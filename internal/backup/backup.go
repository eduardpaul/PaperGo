// Package backup archives the complete SQLite database and immutable local files.
package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
	"papergo/internal/database"
	"papergo/migrations"
)

const format = "papergo-backup/1"
const maxManifest = 16 << 20

type Options struct {
	DatabasePath string
	BlobPath     string
	ArchivePath  string
	Force        bool
}

type fileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type manifest struct {
	Format     string      `json:"format"`
	CreatedAt  time.Time   `json:"created_at"`
	SchemaHash string      `json:"schema_hash"`
	Files      []fileEntry `json:"files"`
}

func paths(in Options) (Options, error) {
	var err error
	for _, p := range []*string{&in.DatabasePath, &in.BlobPath, &in.ArchivePath} {
		if *p == "" {
			return in, errors.New("database, blobs and archive paths are required")
		}
		*p, err = filepath.Abs(*p)
		if err != nil {
			return in, err
		}
		// Reject symlinks in existing path components before replacing or copying.
		for part := *p; ; part = filepath.Dir(part) {
			info, e := os.Lstat(part)
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				return in, e
			}
			if e == nil && info.Mode()&os.ModeSymlink != 0 {
				return in, errors.New("backup paths cannot contain symlinks")
			}
			if filepath.Dir(part) == part {
				break
			}
		}
	}
	inside := func(path, parent string) bool {
		rel, e := filepath.Rel(parent, path)
		return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
	}
	equal := func(a, b string) bool { rel, e := filepath.Rel(a, b); return e == nil && rel == "." }
	reserved := false
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".lease"} {
		reserved = reserved || equal(in.ArchivePath, in.DatabasePath+suffix) || equal(in.BlobPath, in.DatabasePath+suffix)
	}
	if inside(in.DatabasePath, in.BlobPath) || inside(in.ArchivePath, in.BlobPath) || reserved || equal(in.DatabasePath, in.BlobPath+".lease") || equal(in.ArchivePath, in.BlobPath+".lease") {
		return in, errors.New("database, blob directory and archive must not overlap")
	}
	return in, nil
}

func leases(in Options, exclusive bool) (func(), error) {
	data, err := database.AcquireLease(in.DatabasePath, exclusive)
	if err != nil {
		return nil, err
	}
	blobs, err := database.AcquireLease(in.BlobPath, exclusive)
	if err != nil {
		data.Close()
		return nil, err
	}
	return func() { blobs.Close(); data.Close() }, nil
}

// Create can run while the API serves reads. A SQLite writer lock freezes
// reference changes and purge while the online snapshot and files are copied.
func Create(ctx context.Context, in Options) (err error) {
	in, err = paths(in)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(in.ArchivePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("backup destination already exists or cannot be inspected")
	}
	if info, e := os.Stat(in.DatabasePath); e != nil || !info.Mode().IsRegular() {
		return errors.New("source database must be an existing regular file")
	}
	if err = os.MkdirAll(filepath.Dir(in.ArchivePath), 0700); err != nil {
		return err
	}
	release, err := leases(in, false)
	if err != nil {
		return err
	}
	defer release()
	work, err := os.MkdirTemp(filepath.Dir(in.ArchivePath), ".papergo-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	db, err := database.Open(ctx, in.DatabasePath)
	if err != nil {
		return err
	}
	defer db.Close()
	lock, err := db.SQL.Conn(ctx)
	if err != nil {
		return err
	}
	defer lock.Close()
	if _, err = lock.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("freeze backup references: %w", err)
	}
	defer lock.ExecContext(context.Background(), "ROLLBACK")
	snapshot := filepath.Join(work, "database.sqlite")
	if err = snapshotDatabase(ctx, db.SQL, snapshot); err != nil {
		return err
	}
	blobs, err := inspectDatabase(ctx, snapshot)
	if err != nil {
		return err
	}
	size, hash, err := digest(ctx, snapshot)
	if err != nil {
		return err
	}
	m := manifest{Format: format, CreatedAt: time.Now().UTC(), SchemaHash: migrations.SchemaHash(), Files: append([]fileEntry{{Path: "database.sqlite", Size: size, SHA256: hash}}, blobs...)}
	meta, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(meta) > maxManifest {
		return errors.New("backup manifest exceeds 16 MiB")
	}
	archive := filepath.Join(work, "archive.tar.gz")
	f, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(meta)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err = tw.Write(meta); err != nil {
		return err
	}
	for _, entry := range m.Files {
		source := snapshot
		if entry.Path != "database.sqlite" {
			source = filepath.Join(in.BlobPath, strings.TrimPrefix(entry.Path, "blobs/"))
		}
		if err = archiveFile(ctx, tw, source, entry); err != nil {
			return err
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Publish atomically without replacing a backup that appeared meanwhile.
	if err = publishArchive(archive, in.ArchivePath); err != nil {
		return fmt.Errorf("publish backup: %w", err)
	}
	return nil
}

func snapshotDatabase(ctx context.Context, db *sql.DB, path string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) (err error) {
		backup, err := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		}).NewBackup(path)
		if err != nil {
			return err
		}
		defer func() {
			if e := backup.Finish(); err == nil {
				err = e
			}
		}()
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			more, e := backup.Step(256)
			if e != nil {
				return e
			}
			if !more {
				return nil
			}
		}
	})
}

func inspectDatabase(ctx context.Context, path string) ([]fileEntry, error) {
	db, err := database.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err = db.CheckSchema(ctx); err != nil {
		return nil, err
	}
	var hash, integrity string
	if err = db.SQL.QueryRowContext(ctx, "SELECT hash FROM atlas_schema_revisions WHERE version='20261007000100'").Scan(&hash); err != nil || hash != migrations.SchemaHash() {
		return nil, errors.New("database does not match this build's schema")
	}
	if err = db.SQL.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, fmt.Errorf("database integrity check failed: %s %v", integrity, err)
	}
	fk, err := db.SQL.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	bad := fk.Next()
	err = fk.Err()
	fk.Close()
	if err != nil {
		return nil, err
	}
	if bad {
		return nil, errors.New("database foreign key check failed")
	}
	rows, err := db.SQL.QueryContext(ctx, "SELECT DISTINCT object_key,size,sha256 FROM blobs ORDER BY object_key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []fileEntry{}
	for rows.Next() {
		var key string
		var entry fileEntry
		if err = rows.Scan(&key, &entry.Size, &entry.SHA256); err != nil {
			return nil, err
		}
		id, e := uuid.Parse(key)
		if e != nil || key != id.String() || !validDigest(entry.SHA256) || entry.Size < 0 {
			return nil, errors.New("invalid stored file metadata")
		}
		entry.Path = "blobs/" + key
		if len(entries) > 0 && entries[len(entries)-1].Path == entry.Path {
			return nil, errors.New("conflicting stored file metadata")
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func digest(ctx context.Context, path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, contextReader{ctx, f})
	return size, hex.EncodeToString(h.Sum(nil)), err
}

func archiveFile(ctx context.Context, tw *tar.Writer, path string, entry fileEntry) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", entry.Path, err)
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Size {
		return fmt.Errorf("stored file size/type mismatch: %s", entry.Path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = tw.WriteHeader(&tar.Header{Name: entry.Path, Mode: 0600, Size: entry.Size, Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(tw, h), contextReader{ctx, f}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
		return fmt.Errorf("stored file checksum mismatch: %s", entry.Path)
	}
	return nil
}

// Restore validates and stages the whole archive before replacing database and
// files. Exclusive leases reject a running API. Force permits existing data.
func Restore(ctx context.Context, in Options) (err error) {
	in, err = paths(in)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(in.DatabasePath), 0700); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(in.BlobPath), 0700); err != nil {
		return err
	}
	release, err := leases(in, true)
	if err != nil {
		return err
	}
	defer release()
	for _, target := range []string{in.DatabasePath, in.BlobPath, in.DatabasePath + "-wal", in.DatabasePath + "-shm", in.DatabasePath + "-journal"} {
		info, e := os.Lstat(target)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("restore target cannot be a symlink")
			}
			if target == in.BlobPath && !info.IsDir() || target != in.BlobPath && !info.Mode().IsRegular() {
				return errors.New("invalid restore target type")
			}
			if !in.Force {
				return errors.New("restore destination exists; use --force after stopping the API")
			}
		}
	}
	work, err := os.MkdirTemp(filepath.Dir(in.DatabasePath), ".papergo-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	blobs, err := os.MkdirTemp(filepath.Dir(in.BlobPath), ".papergo-blobs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(blobs)
	if err = extract(ctx, in.ArchivePath, work, blobs); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return install(in, filepath.Join(work, "database.sqlite"), blobs)
}

func extract(ctx context.Context, archive, work, blobs string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	gz, err := gzip.NewReader(br)
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(contextReader{ctx, gz})
	header, err := tr.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > maxManifest {
		return errors.New("archive must begin with a regular manifest.json of at most 16 MiB")
	}
	var m manifest
	decoder := json.NewDecoder(tr)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing manifest data")
	}
	if m.Format != format || m.SchemaHash != migrations.SchemaHash() || m.CreatedAt.IsZero() || len(m.Files) < 1 {
		return errors.New("archive format or schema does not match this build")
	}
	expected := map[string]fileEntry{}
	for _, entry := range m.Files {
		if _, duplicate := expected[entry.Path]; duplicate || entry.Size < 0 || !validDigest(entry.SHA256) {
			return errors.New("invalid or duplicate archive file metadata")
		}
		if entry.Path != "database.sqlite" {
			key := strings.TrimPrefix(entry.Path, "blobs/")
			id, e := uuid.Parse(key)
			if !strings.HasPrefix(entry.Path, "blobs/") || e != nil || id.String() != key {
				return errors.New("invalid archive file path")
			}
		}
		expected[entry.Path] = entry
	}
	if _, ok := expected["database.sqlite"]; !ok {
		return errors.New("archive database is missing")
	}
	seen := map[string]bool{}
	for {
		header, err = tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entry, ok := expected[header.Name]
		if !ok || seen[header.Name] || header.Typeflag != tar.TypeReg || header.Size != entry.Size {
			return errors.New("unexpected, duplicate or invalid archive entry")
		}
		seen[header.Name] = true
		target := filepath.Join(work, "database.sqlite")
		if header.Name != "database.sqlite" {
			target = filepath.Join(blobs, strings.TrimPrefix(header.Name, "blobs/"))
		}
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		h := sha256.New()
		size, e := io.Copy(io.MultiWriter(out, h), tr)
		if e == nil {
			e = out.Sync()
		}
		closeErr := out.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return e
		}
		if size != entry.Size || hex.EncodeToString(h.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("archive checksum mismatch: %s", entry.Path)
		}
	}
	if len(seen) != len(expected) {
		return errors.New("archive is incomplete")
	}
	// Consume the gzip trailer to verify CRC, and reject appended archives.
	var extra [1]byte
	if n, e := gz.Read(extra[:]); n != 0 || e != io.EOF {
		return errors.New("invalid archive trailer")
	}
	if _, e := br.Peek(1); e != io.EOF {
		return errors.New("trailing data after archive")
	}
	actual, err := inspectDatabase(ctx, filepath.Join(work, "database.sqlite"))
	if err != nil {
		return err
	}
	want := []fileEntry{}
	for _, entry := range m.Files {
		if entry.Path != "database.sqlite" {
			want = append(want, entry)
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i].Path < want[j].Path })
	if len(actual) != len(want) {
		return errors.New("archive files do not match database references")
	}
	for i := range actual {
		if actual[i] != want[i] {
			return errors.New("archive file metadata does not match database")
		}
	}
	return nil
}

func install(in Options, snapshot, blobs string) (err error) {
	type moved struct{ from, to string }
	old := []moved{}
	installed := []string{}
	// Keep old files beside their destination for same-volume renames.
	defer func() {
		if err != nil {
			for i := len(installed) - 1; i >= 0; i-- {
				if e := os.RemoveAll(installed[i]); e != nil {
					err = errors.Join(err, e)
				}
			}
			for i := len(old) - 1; i >= 0; i-- {
				if e := os.Rename(old[i].to, old[i].from); e != nil {
					err = errors.Join(err, fmt.Errorf("recover %s from %s: %w", old[i].from, old[i].to, e))
				}
			}
		} else {
			for _, entry := range old {
				if e := os.RemoveAll(entry.to); e != nil {
					err = errors.Join(err, fmt.Errorf("restore completed; remove saved destination %s: %w", entry.to, e))
				}
			}
		}
	}()
	for _, target := range []string{in.DatabasePath, in.DatabasePath + "-wal", in.DatabasePath + "-shm", in.DatabasePath + "-journal", in.BlobPath} {
		if _, e := os.Lstat(target); errors.Is(e, os.ErrNotExist) {
			continue
		} else if e != nil {
			return e
		}
		saved := target + ".restore-" + uuid.NewString()
		if err = os.Rename(target, saved); err != nil {
			return err
		}
		old = append(old, moved{target, saved})
	}
	if err = os.Rename(snapshot, in.DatabasePath); err != nil {
		return err
	}
	installed = append(installed, in.DatabasePath)
	if err = os.Rename(blobs, in.BlobPath); err != nil {
		return err
	}
	installed = append(installed, in.BlobPath)
	return nil
}
