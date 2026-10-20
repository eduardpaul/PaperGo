package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"papergo/internal/documenttext"
)

// textPage is one page of a file's text.
type textPage struct {
	Page int    `json:"page"`
	Text string `json:"text"`
	OCR  bool   `json:"ocr,omitempty"`
}

// itemText returns the text read from the file version the caller may
// read: the current one, or blob_id.
func (a *API) itemText(w http.ResponseWriter, r *http.Request) {
	b, text, err := a.DMS.ItemText(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	file, err := a.Storage.Open(r.Context(), text.ObjectKey)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	defer file.Close()
	var stored struct {
		Pages []textPage `json:"pages"`
	}
	if err = json.NewDecoder(io.LimitReader(file, text.ObjectSize)).Decode(&stored); err != nil {
		a.failure(w, r, err)
		return
	}
	if stored.Pages == nil {
		stored.Pages = []textPage{}
	}
	w.Header().Set("ETag", strconv.Quote(text.ID))
	respond(w, r, 200, map[string]any{
		"item_id": r.PathValue("id"), "blob_id": b.ID, "text_id": text.ID, "pages": stored.Pages, "page_count": text.Pages, "chars": text.Chars,
		"language": text.Language, "ocr_pages": text.OcrPages, "skipped_pages": text.SkippedPages, "truncated": text.Truncated,
		"read_at": text.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// readText starts reading the text of the item's current file and answers
// with the pending operation. The body is optional.
func (a *API) readText(w http.ResponseWriter, r *http.Request) {
	if a.DocumentText == nil {
		a.problem(w, r, http.StatusServiceUnavailable, "unavailable", "reading document text is not configured")
		return
	}
	var in documenttext.Request
	if r.ContentLength != 0 && !a.decode(w, r, &in) {
		return
	}
	op, err := a.DocumentText.Start(r.Context(), a.DMS, subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/operations/"+op.ID)
	respond(w, r, http.StatusAccepted, op)
}
