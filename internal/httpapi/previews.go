package httpapi

import (
	"net/http"
	"strconv"

	"papergo/internal/transfer"
)

func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	file, preview, err := a.DMS.ItemPreview(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	if preview == nil {
		w.Header().Set("Retry-After", "2")
		respond(w, r, 202, map[string]any{"status": "pending", "blob_id": file.ID})
		return
	}
	if preview.Status != "ready" {
		respond(w, r, 200, map[string]any{"status": "unavailable", "blob_id": file.ID})
		return
	}
	data, err := a.Storage.Open(r.Context(), preview.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer data.Close()
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Content-Disposition", `inline; filename="preview.webp"`)
	w.Header().Set("ETag", strconv.Quote(preview.ObjectSha256))
	w.Header().Set("X-Preview-Blob-ID", file.ID)
	http.ServeContent(transfer.Writer(w), r, "preview.webp", preview.CreatedAt, data)
}
