package webdav

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"papergo/internal/storage"
)

// Scratch files are the housekeeping files operating systems and office
// suites write next to the files people work on: Office owner files (~$),
// LibreOffice lock files, Windows thumbnails and folder settings, macOS
// folder state and resource forks. They are kept in memory for an hour and
// never become items, so they neither clutter libraries nor start workflows.
// Each person, and the server as a whole, holds a bounded number and size
// of them, so nobody can fill the server's memory or crowd others out.
const (
	scratchMaxBytes = 1 << 20
	scratchLifetime = time.Hour
	scratchMaxFiles = 10_000
	scratchMaxTotal = 128 << 20
	// A person copying a thousand files from a Mac writes a thousand ._ files of 4 KB.
	scratchOwnerFiles = 2_000
	scratchOwnerBytes = 16 << 20
)

// errScratchFull refuses a housekeeping file over a person's or the server's bound.
var errScratchFull = errors.New("no room for more housekeeping files")

// scratchName reports whether a file name is housekeeping.
func scratchName(name string) bool {
	lower := strings.ToLower(name)
	switch lower {
	case "thumbs.db", "desktop.ini", ".ds_store":
		return true
	}
	return strings.HasPrefix(name, "~$") || strings.HasPrefix(name, "._") || strings.HasPrefix(name, ".~lock.") && strings.HasSuffix(name, "#")
}

type scratchFile struct {
	owner    string
	data     []byte
	sha256   string
	created  time.Time
	modified time.Time
}

type scratchArea struct {
	mu    sync.Mutex
	files map[string]*scratchFile
	// bytes is the size of every file held; owners counts each owner's files and bytes.
	bytes  int
	owners map[string]*scratchUse
}

type scratchUse struct{ files, bytes int }

func newScratchArea() *scratchArea {
	return &scratchArea{files: map[string]*scratchFile{}, owners: map[string]*scratchUse{}}
}

// expire drops files untouched for scratchLifetime; callers hold mu.
func (a *scratchArea) expire(now time.Time) {
	for key, f := range a.files {
		if now.Sub(f.modified) > scratchLifetime {
			a.remove(key)
		}
	}
}

// remove drops the file at key and what it counted; callers hold mu.
func (a *scratchArea) remove(key string) bool {
	f, ok := a.files[key]
	if !ok {
		return false
	}
	delete(a.files, key)
	a.bytes -= len(f.data)
	use := a.owners[f.owner]
	use.files--
	use.bytes -= len(f.data)
	if use.files == 0 {
		delete(a.owners, f.owner)
	}
	return true
}

func (a *scratchArea) get(key string) (scratchFile, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expire(time.Now())
	f, ok := a.files[key]
	if !ok {
		return scratchFile{}, false
	}
	return *f, true
}

// put stores data at key for owner, who then holds it, and reports
// whether it created the file.
func (a *scratchArea) put(key, owner string, data []byte) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.expire(now)
	old, replaced := a.files[key]
	use := a.owners[owner]
	if use == nil {
		use = &scratchUse{}
	}
	// What the file would add for its new owner, and to the server's total.
	files, bytes, total := 1, len(data), len(data)
	if replaced {
		total -= len(old.data)
		if old.owner == owner {
			files, bytes = 0, len(data)-len(old.data)
		}
	}
	if use.files+files > scratchOwnerFiles || use.bytes+bytes > scratchOwnerBytes ||
		!replaced && len(a.files) >= scratchMaxFiles || a.bytes+total > scratchMaxTotal {
		return false, errScratchFull
	}
	created := now
	if replaced {
		created = old.created
		a.remove(key)
	}
	sum := sha256.Sum256(data)
	a.files[key] = &scratchFile{owner: owner, data: data, sha256: hex.EncodeToString(sum[:]), created: created, modified: now}
	a.bytes += len(data)
	use.files++
	use.bytes += len(data)
	a.owners[owner] = use
	return !replaced, nil
}

func (a *scratchArea) delete(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.remove(key)
}

// file describes a scratch file the way PROPFIND describes items.
func (f scratchFile) file(name string) *dms.File {
	return &dms.File{
		Resource: &resources.Resource{ID: "scratch", Kind: refs.KindItem, Name: name, CreatedAt: f.created, UpdatedAt: f.modified},
		Blob:     &blobs.Blob{Size: int64(len(f.data)), ContentType: blobs.MediaType(storage.OctetStream), Sha256: f.sha256},
	}
}

// scratch serves a housekeeping file. Reading needs read access to the
// folder it is in, writing needs write access there, as for real files.
func (h *Handler) scratch(w http.ResponseWriter, r *request) error {
	folder, name := r.path[:len(r.path)-1], r.path[len(r.path)-1]
	key := lockKey(r.library, r.path)
	switch r.Method {
	case http.MethodGet, http.MethodHead, "PROPFIND":
		if _, err := h.DMS.LibraryFile(r.Context(), r.subject, r.library, folder); err != nil {
			return err
		}
		f, ok := h.scratchFiles.get(key)
		if !ok {
			return dms.ErrNotFound
		}
		if r.Method == "PROPFIND" {
			q, err := readPropfind(r)
			if err != nil {
				return err
			}
			b := multistatus(w)
			h.writeResponse(b, r.library, r.path, f.file(name), q, h.locks.snapshot())
			b.WriteString(`</D:multistatus>`)
			return b.Flush()
		}
		w.Header().Set("Content-Type", storage.OctetStream)
		w.Header().Set("ETag", f.file(name).ETag())
		http.ServeContent(w, r.Request, name, f.modified, bytes.NewReader(f.data))
		return nil
	}
	if _, err := h.DMS.WriteTarget(r.Context(), r.subject, r.library, folder, dms.FileConditions{}); err != nil {
		return err
	}
	// Writes honour locks as on real files: Office tells who has a document open by its locked owner file.
	if r.Method == http.MethodPut || r.Method == http.MethodDelete {
		if err := h.confirm(r, r.path, false); err != nil {
			return err
		}
	}
	switch r.Method {
	case http.MethodPut:
		if r.ContentLength > scratchMaxBytes {
			return storage.ErrTooLarge
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, scratchMaxBytes))
		if err != nil {
			return err
		}
		created, err := h.scratchFiles.put(key, r.subject, data)
		if err != nil {
			return err
		}
		if created {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case http.MethodDelete:
		if !h.scratchFiles.delete(key) {
			return dms.ErrNotFound
		}
		h.locks.release(key)
		w.WriteHeader(http.StatusNoContent)
	// Clients lock and annotate their owner files like any other file.
	case "LOCK":
		return h.lock(w, r)
	case "UNLOCK":
		return h.unlock(w, r)
	case "PROPPATCH":
		return h.proppatch(w, r)
	default:
		w.Header().Set("Allow", "OPTIONS, GET, HEAD, PUT, DELETE, PROPFIND, PROPPATCH, LOCK, UNLOCK")
		h.status(w, http.StatusMethodNotAllowed, "housekeeping files cannot be copied, moved or made folders")
	}
	return nil
}
