package webdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"papergo/internal/dal/blobs"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/transfer"
	"slices"
	"strings"
)

func (h *Handler) get(w http.ResponseWriter, r *request) error {
	f, err := h.DMS.LibraryFile(r.Context(), r.subject, r.library, r.path)
	if err != nil {
		return err
	}
	if f.Collection() {
		w.Header().Set("Allow", "OPTIONS, PROPFIND, PROPPATCH, MKCOL, DELETE, COPY, MOVE, LOCK, UNLOCK")
		h.status(w, http.StatusMethodNotAllowed, "folders have no content; use PROPFIND")
		return nil
	}
	var content io.ReadSeeker = bytes.NewReader(nil)
	if f.Blob != nil && f.Blob.Availability == blobs.AvailabilityReclaimed {
		return dms.ErrContentUnavailable
	}
	if f.Blob != nil {
		file, err := h.Storage.Open(r.Context(), f.Blob.ObjectKey)
		if err != nil {
			return err
		}
		defer file.Close()
		content = file
	}
	w.Header().Set("Content-Type", f.ContentType())
	// File clients ignore the disposition; browsers must not render uploads inline.
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	transfer.StoredFile(w)
	w.Header().Set("ETag", f.ETag())
	http.ServeContent(transfer.Writer(w), r.Request, f.Name, f.UpdatedAt, content)
	return nil
}

func (h *Handler) put(w http.ResponseWriter, r *request) error {
	if len(r.path) == 0 {
		h.status(w, http.StatusMethodNotAllowed, "the library is a folder")
		return nil
	}
	if r.Header.Get("Content-Range") != "" {
		return badRequest("partial PUT is not supported")
	}
	if err := h.confirm(r, r.path, false); err != nil {
		return err
	}
	cond := dms.FileConditions{IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match")}
	// Authorize before storing any bytes; PutFile repeats the checks atomically.
	existing, err := h.DMS.WriteTarget(r.Context(), r.subject, r.library, r.path, cond)
	if err != nil {
		return err
	}
	if existing != nil && existing.Collection() {
		return dms.ErrExists
	}
	if r.ContentLength > h.MaxUpload {
		return storage.ErrTooLarge
	}
	body := transfer.Reader(http.MaxBytesReader(w, r.Body, h.MaxUpload), w)
	f, created, err := h.store(r, r.path, body, cond, nil, nil, "")
	if err != nil {
		return err
	}
	w.Header().Set("ETag", f.ETag())
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
	return nil
}

// store writes body to blob storage, then commits it as the file at path.
// Bytes of a rejected commit are removed again.
func (h *Handler) store(r *request, path []string, body io.Reader, cond dms.FileConditions, keywords []string, values map[string]any, typeID string) (*dms.File, bool, error) {
	object, mediaType, err := storage.PutDocument(r.Context(), h.Storage, body, h.MaxUpload, path[len(path)-1])
	if err != nil {
		return nil, false, err
	}
	in := dms.PutFile{FileConditions: cond, Keywords: keywords, Values: values, ContentTypeID: typeID, Blob: dms.BlobInput{ObjectKey: object.Key, Filename: path[len(path)-1], ContentType: mediaType, Size: object.Size, SHA256: object.SHA256, Source: dms.FileWebDAV}}
	f, created, err := h.DMS.PutFile(r.Context(), r.subject, r.library, path, in)
	if err != nil {
		if cleanupErr := h.Storage.Delete(context.WithoutCancel(r.Context()), object.Key); cleanupErr != nil {
			h.Logger.Error("orphan cleanup failed", "object_key", object.Key, "error", cleanupErr)
		}
		return nil, false, err
	}
	return f, created, nil
}

func (h *Handler) mkcol(w http.ResponseWriter, r *request) error {
	if len(r.path) == 0 {
		return dms.ErrExists
	}
	// MKCOL bodies would describe a collection to create; none are supported.
	if n, _ := r.Body.Read(make([]byte, 1)); n > 0 || r.ContentLength > 0 {
		h.status(w, http.StatusUnsupportedMediaType, "MKCOL does not accept a body")
		return nil
	}
	if err := h.confirm(r, r.path, false); err != nil {
		return err
	}
	if _, err := h.DMS.MakeFolder(r.Context(), r.subject, r.library, r.path); err != nil {
		return err
	}
	w.WriteHeader(http.StatusCreated)
	return nil
}

func (h *Handler) delete(w http.ResponseWriter, r *request) error {
	if len(r.path) == 0 {
		return dms.ErrForbidden
	}
	if err := h.confirm(r, r.path, true); err != nil {
		return err
	}
	cond := dms.FileConditions{IfMatch: r.Header.Get("If-Match"), IfNoneMatch: r.Header.Get("If-None-Match")}
	if err := h.DMS.DeleteFile(r.Context(), r.subject, r.library, r.path, cond); err != nil {
		return err
	}
	h.locks.release(lockKey(r.library, r.path))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *Handler) copyMove(w http.ResponseWriter, r *request) error {
	dst, err := h.destination(r)
	if err != nil {
		return err
	}
	if len(r.path) == 0 || len(dst) == 0 || strings.Join(r.path, "/") == strings.Join(dst, "/") {
		return dms.ErrForbidden
	}
	// Names compare like the library compares them. Only MOVE may target the
	// source's own name (a case-only rename); replacing an ancestor or entering
	// the source itself would destroy the source before it is read.
	same := lockKey("", dst) == lockKey("", r.path)
	switch {
	case same && r.Method == "COPY":
		return refused("source and destination are the same entry")
	case inside(r.path, dst):
		return refused("destination contains the source")
	case inside(dst, r.path):
		return refused("destination is inside the source")
	}
	overwrite := true
	switch r.Header.Get("Overwrite") {
	case "", "T":
	case "F":
		overwrite = false
	default:
		return badRequest("Overwrite must be T or F")
	}
	depth := r.Header.Get("Depth")
	var created bool
	if r.Method == "MOVE" {
		if depth != "" && depth != "infinity" {
			return badRequest("MOVE depth must be infinity")
		}
		if err = h.confirm(r, r.path, true); err != nil {
			return err
		}
		if err = h.confirm(r, dst, true); err != nil {
			return err
		}
		if created, err = h.DMS.MoveFile(r.Context(), r.subject, r.library, r.path, dst, overwrite); err != nil {
			return err
		}
		h.locks.release(lockKey(r.library, r.path))
	} else {
		if depth != "" && depth != "infinity" && depth != "0" {
			return badRequest("COPY depth must be 0 or infinity")
		}
		if err = h.confirm(r, dst, true); err != nil {
			return err
		}
		if created, err = h.copy(r, dst, overwrite, depth != "0"); err != nil {
			return err
		}
	}
	if created {
		w.WriteHeader(http.StatusCreated)
	} else {
		w.WriteHeader(http.StatusNoContent)
	}
	return nil
}

// destination parses the Destination header, which must name the same library
// on this server: the request's host or, behind a proxy, the public host.
func (h *Handler) destination(r *request) ([]string, error) {
	raw := r.Header.Get("Destination")
	if raw == "" {
		return nil, badRequest("Destination is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, badRequest("invalid Destination")
	}
	if u.Host != "" && !strings.EqualFold(u.Host, r.Host) && !strings.EqualFold(u.Host, h.PublicHost) || !strings.HasPrefix(u.EscapedPath(), Prefix) {
		return nil, errBadGateway
	}
	library, dst, ok := parsePath(u.EscapedPath())
	if !ok {
		return nil, badRequest("invalid Destination")
	}
	if library != r.library {
		return nil, errBadGateway
	}
	return dst, nil
}

// maxCopyEntries bounds one recursive COPY, which is not a single transaction.
const maxCopyEntries = dms.MaxFolderEntries

// copy duplicates the source into dst: files gain new items carrying the
// source's keywords, values and bytes; an existing destination file instead gets a
// new content revision. Collections are recreated, recursively for infinity.
func (h *Handler) copy(r *request, dst []string, overwrite, infinite bool) (bool, error) {
	src, err := h.DMS.LibraryFile(r.Context(), r.subject, r.library, r.path)
	if err != nil {
		return false, err
	}
	existing, err := h.DMS.LibraryFile(r.Context(), r.subject, r.library, dst)
	if errors.Is(err, dms.ErrNotFound) {
		existing, err = nil, nil
	}
	if err != nil {
		return false, err
	}
	// A reader's published name can resolve the destination to the source itself.
	if existing != nil && existing.ID == src.ID {
		return false, refused("source and destination are the same entry")
	}
	if existing != nil {
		if !overwrite {
			return false, dms.ErrPreconditionFailed
		}
		if existing.Collection() || src.Collection() {
			if err = h.DMS.DeleteFile(r.Context(), r.subject, r.library, dst, dms.FileConditions{}); err != nil {
				return false, err
			}
			h.locks.release(lockKey(r.library, dst))
		}
	}
	budget := maxCopyEntries
	return existing == nil, h.copyTree(r, src, r.path, dst, infinite, &budget)
}
func (h *Handler) copyTree(r *request, f *dms.File, from, to []string, infinite bool, budget *int) error {
	if *budget == 0 {
		return badRequest("copy exceeds the entry limit")
	}
	*budget--
	if !f.Collection() {
		if f.Blob == nil {
			_, err := h.DMS.MakeEmptyFile(r.Context(), r.subject, r.library, to, dms.CreateResource{Keywords: f.Keywords, Values: f.Values, ContentTypeID: *f.ContentTypeID}, dms.FileConditions{})
			return err
		}
		var content io.Reader
		if f.Blob != nil {
			file, err := h.Storage.Open(r.Context(), f.Blob.ObjectKey)
			if err != nil {
				return err
			}
			defer file.Close()
			content = file
		}
		typ, err := h.DMS.CollectionContentType(r.Context(), r.subject, r.library, *f.Resource.ContentTypeID)
		if err != nil {
			return err
		}
		values := map[string]any{}
		defs, err := h.DMS.Fields(r.Context(), r.subject, r.library)
		if err != nil {
			return err
		}
		for _, d := range defs {
			if !slices.Contains(typ.Effective.FieldIds, d.ID) {
				continue
			}
			key := d.Key
			if value, ok := f.Values[key]; ok {
				values[key] = value
			}
		}
		_, _, err = h.store(r, to, content, dms.FileConditions{}, f.Keywords, values, typ.ID)
		return err
	}
	if _, err := h.DMS.MakeFolder(r.Context(), r.subject, r.library, to); err != nil {
		return err
	}
	if !infinite {
		return nil
	}
	_, entries, err := h.DMS.LibraryFolder(r.Context(), r.subject, r.library, from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err = h.copyTree(r, e, append(from[:len(from):len(from)], e.Name), append(to[:len(to):len(to)], e.Name), infinite, budget); err != nil {
			return err
		}
	}
	return nil
}

// inside reports whether path lies below root, comparing names like the library does.
func inside(path, root []string) bool {
	return len(path) > len(root) && lockKey("", path[:len(root)]) == lockKey("", root)
}
