package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/transfer"
)

func blobResponse(b *ent.Blob, resourceVersion int) map[string]any {
	return map[string]any{"id": b.ID, "item_id": b.ItemID, "version": b.Version, "resource_version": resourceVersion, "filename": b.Filename, "content_type": b.ContentType, "size": b.Size, "sha256": b.Sha256, "languages": b.Languages}
}

const documentMetadataLimit int64 = 64 << 10

func (a *API) createDocument(w http.ResponseWriter, r *http.Request) {
	if err := a.DMS.CanCreateDocument(r.Context(), subject(r), r.PathValue("id")); err != nil {
		a.failure(w, r, err)
		return
	}
	limit := a.MaxUpload + 2*documentMetadataLimit
	if r.ContentLength > limit {
		a.failure(w, r, storage.ErrTooLarge)
		return
	}
	// Extend transfer deadlines for boundaries and metadata as well as file bytes.
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	defer r.Body.Close()
	r.Body = io.NopCloser(transfer.Reader(r.Body, w))
	reader, err := r.MultipartReader()
	if err != nil {
		a.problem(w, r, 400, "invalid_upload", "multipart/form-data is required")
		return
	}
	var metadata dms.DocumentMetadata
	var object storage.Object
	var filename, media string
	committed := false
	defer func() {
		if object.Key != "" && !committed {
			if err := a.Storage.Delete(context.Background(), object.Key); err != nil {
				a.Logger.Error("orphan cleanup failed", "object_key", object.Key, "error", err)
			}
		}
	}()
	seen := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var max *http.MaxBytesError
			if errors.As(err, &max) {
				a.failure(w, r, err)
			} else {
				a.problem(w, r, 400, "invalid_upload", "invalid multipart body")
			}
			return
		}
		name := part.FormName()
		if (name != "file" && name != "metadata") || seen[name] {
			part.Close()
			a.problem(w, r, 400, "invalid_upload", "use one file part and at most one metadata part")
			return
		}
		seen[name] = true
		if name == "file" {
			filename = path.Base(strings.ReplaceAll(part.FileName(), `\`, "/"))
			if filename == "" || filename == "." || filename == "/" {
				part.Close()
				a.problem(w, r, 400, "invalid_upload", "file must have a filename")
				return
			}
			object, media, err = storage.PutDocument(r.Context(), a.Storage, part, a.MaxUpload)
			if err != nil {
				if errors.Is(err, io.ErrUnexpectedEOF) {
					a.problem(w, r, 400, "invalid_upload", "invalid multipart file")
				} else {
					a.failure(w, r, err)
				}
				return
			}
		} else {
			if part.FileName() != "" {
				part.Close()
				a.problem(w, r, 400, "invalid_upload", "metadata must be a JSON form field")
				return
			}
			raw, e := io.ReadAll(io.LimitReader(part, documentMetadataLimit+1))
			if e != nil {
				var max *http.MaxBytesError
				if errors.As(e, &max) {
					a.failure(w, r, e)
				} else {
					a.problem(w, r, 400, "invalid_upload", "could not read metadata")
				}
				return
			}
			if int64(len(raw)) > documentMetadataLimit {
				a.failure(w, r, storage.ErrTooLarge)
				return
			}
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&metadata); err != nil {
				a.problem(w, r, 400, "invalid_upload", "metadata must be one JSON object with known fields")
				return
			}
			var trailing any
			if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
				a.problem(w, r, 400, "invalid_upload", "metadata must be one JSON object")
				return
			}
			if strings.TrimSpace(string(raw)) == "null" {
				a.problem(w, r, 400, "invalid_upload", "metadata must be a JSON object")
				return
			}
		}
		if err = part.Close(); err != nil {
			a.problem(w, r, 400, "invalid_upload", "invalid multipart body")
			return
		}
	}
	if !seen["file"] {
		a.problem(w, r, 400, "invalid_upload", "a file part is required")
		return
	}
	out, err := a.DMS.CreateDocument(r.Context(), subject(r), r.PathValue("id"), metadata, dms.BlobInput{ObjectKey: object.Key, Filename: filename, ContentType: media, Size: object.Size, SHA256: object.SHA256})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	committed = true
	etag(w, out.Item.Version)
	w.Header().Set("Location", "/v1/resources/"+out.Item.ID)
	respond(w, r, 201, map[string]any{"item": out.Item, "file": blobResponse(out.File, out.Item.Version), "duplicates": out.Duplicates})
}

func (a *API) restoreBlob(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil || number < 1 {
		a.failure(w, r, dms.ErrNotFound)
		return
	}
	out, err := a.DMS.RestoreBlob(r.Context(), subject(r), r.PathValue("id"), number, version)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, version+1)
	respond(w, r, http.StatusOK, blobResponse(out, version+1))
}
