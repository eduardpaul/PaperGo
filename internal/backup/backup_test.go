package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/testutil"
	"papergo/migrations"
)

func fixture(t *testing.T) (Options, *database.Database, string, string) {
	t.Helper()
	root := t.TempDir()
	in := Options{DatabasePath: filepath.Join(root, "papergo.db"), BlobPath: filepath.Join(root, "blobs"), ArchivePath: filepath.Join(root, "backup.tar.gz")}
	db := testutil.DatabaseAt(t, in.DatabasePath)
	if _, err := db.SQL.Exec(`CREATE TABLE atlas_schema_revisions(version TEXT,hash TEXT,applied INTEGER,total INTEGER,error TEXT); INSERT INTO atlas_schema_revisions VALUES('20261007000100',?,1,1,NULL); CREATE TABLE runner_checkpoint(id TEXT PRIMARY KEY,state TEXT); INSERT INTO runner_checkpoint VALUES('durable','waiting')`, migrations.SchemaHash()); err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Backup"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Document"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(in.BlobPath)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(ctx, strings.NewReader("%PDF-saved bytes"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AttachBlob(ctx, "alice", i.ID, i.Version, dms.BlobInput{ObjectKey: object.Key, Filename: "note.pdf", ContentType: "application/pdf", Size: object.Size, SHA256: object.SHA256}); err != nil {
		t.Fatal(err)
	}
	return in, db, i.ID, object.Key
}

func TestLiveBackupAndRestoreCompleteState(t *testing.T) {
	in, _, item, key := fixture(t)
	lease, err := database.AcquireLease(in.DatabasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	// The open WAL-backed source and shared API lease remain active during backup.
	if err = Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if err = Create(context.Background(), in); err == nil {
		t.Fatal("existing backup overwritten")
	}
	root := t.TempDir()
	dest := Options{DatabasePath: filepath.Join(root, "restored.db"), BlobPath: filepath.Join(root, "files"), ArchivePath: in.ArchivePath}
	if err = Restore(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	restored, err := database.Open(context.Background(), dest.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	var name, state string
	if err = restored.SQL.QueryRow("SELECT name FROM resources WHERE id=?", item).Scan(&name); err != nil || name != "Document" {
		t.Fatal(name, err)
	}
	if err = restored.SQL.QueryRow("SELECT state FROM runner_checkpoint WHERE id='durable'").Scan(&state); err != nil || state != "waiting" {
		t.Fatal(state, err)
	}
	if err = restored.CheckSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored.Close()
	data, err := os.ReadFile(filepath.Join(dest.BlobPath, key))
	if err != nil || string(data) != "%PDF-saved bytes" {
		t.Fatal(string(data), err)
	}
	if err = Restore(context.Background(), dest); err == nil {
		t.Fatal("existing data replaced without force")
	}
	if err = os.WriteFile(filepath.Join(dest.BlobPath, "orphan"), []byte("unused"), 0600); err != nil {
		t.Fatal(err)
	}
	dest.Force = true
	if err = Restore(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dest.BlobPath, "orphan")); !os.IsNotExist(err) {
		t.Fatal("restore did not replace complete file store", err)
	}
}

func TestBackupRejectsMissingOrCorruptFilesAndSchema(t *testing.T) {
	in, db, _, key := fixture(t)
	if err := os.WriteFile(filepath.Join(in.BlobPath, key), []byte("wrong bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), in); err == nil {
		t.Fatal("corrupt file accepted")
	}
	if _, err := os.Stat(in.ArchivePath); !os.IsNotExist(err) {
		t.Fatal("failed backup published", err)
	}
	if err := os.Remove(filepath.Join(in.BlobPath, key)); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), in); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := db.SQL.Exec("UPDATE atlas_schema_revisions SET hash='other'"); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), in); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatal(err)
	}
}

func TestRestoreRejectsRunningAPI(t *testing.T) {
	in, db, _, _ := fixture(t)
	if err := Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	lease, err := database.AcquireLease(in.DatabasePath, false)
	if err != nil {
		t.Fatal(err)
	}
	in.Force = true
	if err = Restore(context.Background(), in); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatal(err)
	}
	lease.Close()
	db.Close()
	if err = Restore(context.Background(), in); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreValidatesArchiveBeforeReplacingAnything(t *testing.T) {
	in, _, _, _ := fixture(t)
	if err := Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(in.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dest := Options{DatabasePath: filepath.Join(root, "target.db"), BlobPath: filepath.Join(root, "files"), ArchivePath: filepath.Join(root, "bad.tar.gz"), Force: true}
	if err = os.WriteFile(dest.DatabasePath, []byte("existing database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(dest.BlobPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dest.BlobPath, "existing"), []byte("existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"truncated": valid[:len(valid)/2], "trailing": append(append([]byte{}, valid...), []byte("junk")...)}
	for _, mode := range []string{"traversal", "symlink", "duplicate", "checksum", "missing"} {
		cases[mode] = rewriteArchive(t, valid, mode)
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(dest.ArchivePath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := Restore(context.Background(), dest); err == nil {
				t.Fatal("invalid archive accepted")
			}
			got, err := os.ReadFile(dest.DatabasePath)
			if err != nil || string(got) != "existing database" {
				t.Fatal("destination changed", string(got), err)
			}
			got, err = os.ReadFile(filepath.Join(dest.BlobPath, "existing"))
			if err != nil || string(got) != "existing file" {
				t.Fatal(string(got), err)
			}
		})
	}
}

func rewriteArchive(t *testing.T, data []byte, mode string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	tw := tar.NewWriter(zw)
	for index := 0; ; index++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if index == 1 {
			switch mode {
			case "traversal":
				h.Name = "../escaped"
			case "symlink":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "outside"
				h.Size = 0
				body = nil
			case "checksum":
				body[0] ^= 1
			case "missing":
				continue
			}
		}
		if e = tw.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if _, e = tw.Write(body); e != nil {
			t.Fatal(e)
		}
		if index == 1 && mode == "duplicate" {
			if e = tw.WriteHeader(h); e != nil {
				t.Fatal(e)
			}
			if _, e = tw.Write(body); e != nil {
				t.Fatal(e)
			}
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestInstallRollsBackBothDestinationsOnFailure(t *testing.T) {
	root := t.TempDir()
	in := Options{DatabasePath: filepath.Join(root, "target.db"), BlobPath: filepath.Join(root, "files")}
	if err := os.WriteFile(in.DatabasePath, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(in.BlobPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.BlobPath, "old"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "new.db")
	if err := os.WriteFile(snapshot, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := install(in, snapshot, filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing staged files accepted")
	}
	got, err := os.ReadFile(in.DatabasePath)
	if err != nil || string(got) != "old" {
		t.Fatal(string(got), err)
	}
	if _, err = os.Stat(filepath.Join(in.BlobPath, "old")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoresIntegrationEncryptionKey(t *testing.T) {
	in, _, _, _ := fixture(t)
	key := bytes.Repeat([]byte{42}, 32)
	if err := os.WriteFile(in.DatabasePath+".integrations.key", key, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Create(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dest := Options{DatabasePath: filepath.Join(root, "restored.db"), BlobPath: filepath.Join(root, "blobs"), ArchivePath: in.ArchivePath}
	if err := Restore(t.Context(), dest); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(dest.DatabasePath + ".integrations.key")
	if err != nil || !bytes.Equal(restored, key) {
		t.Fatal("key was not restored", err)
	}
}
