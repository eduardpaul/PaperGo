package webdav

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
const (
	scratchMaxBytes = 1 << 20
	scratchLifetime = time.Hour
	scratchMaxFiles = 10_000
)

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
	data     []byte
	sha256   string
	created  time.Time
	modified time.Time
}

type scratchArea struct {
	mu    sync.Mutex
	files map[string]*scratchFile
}

func newScratchArea() *scratchArea { return &scratchArea{files: map[string]*scratchFile{}} }

// expire drops files untouched for scratchLifetime.
func (a *scratchArea) expire(now time.Time) {
	for key, f := range a.files {
		if now.Sub(f.modified) > scratchLifetime {
			delete(a.files, key)
		}
	}
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

// put stores data at key and reports whether it created the file.
func (a *scratchArea) put(key string, data []byte) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	a.expire(now)
	sum := sha256.Sum256(data)
	if f, ok := a.files[key]; ok {
		f.data, f.sha256, f.modified = data, hex.EncodeToString(sum[:]), now
		return false, nil
	}
	if len(a.files) >= scratchMaxFiles {
		return false, errTooManyLocks
	}
	a.files[key] = &scratchFile{data: data, sha256: hex.EncodeToString(sum[:]), created: now, modified: now}
	return true, nil
}

func (a *scratchArea) delete(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.files[key]
	delete(a.files, key)
	return ok
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
	switch r.Method {
	case http.MethodPut:
		if r.ContentLength > scratchMaxBytes {
			return storage.ErrTooLarge
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, scratchMaxBytes))
		if err != nil {
			return err
		}
		created, err := h.scratchFiles.put(key, data)
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
