package httpapi

import (
	"net/http"
	"strconv"

	"papergo/ent"
	"papergo/internal/dms"
)

func blobResponse(b *ent.Blob, resourceVersion int) map[string]any {
	return map[string]any{"id": b.ID, "item_id": b.ItemID, "version": b.Version, "resource_version": resourceVersion, "filename": b.Filename, "content_type": b.ContentType, "size": b.Size, "sha256": b.Sha256}
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
	respond(w, http.StatusOK, blobResponse(out, version+1))
}
