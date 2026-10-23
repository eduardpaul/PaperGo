package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"papergo/internal/dms"
)

// textPage is one page of a file's text.
type textPage struct {
	Page int    `json:"page"`
	Text string `json:"text"`
	OCR  bool   `json:"ocr,omitempty"`
	// Partial marks a page a ranged read cut.
	Partial bool `json:"partial,omitempty"`
}

// textPosition is where a ranged read of a file's text starts or resumes:
// a page, and characters into it.
type textPosition struct {
	Page   int `json:"page"`
	Offset int `json:"offset"`
}

// textRange selects a part of a file's text: pages from from, up to limit
// characters (Unicode code points) when limit is positive. next is where
// the following part starts, nil after the end.
func textRange(pages []textPage, from textPosition, limit int) (out []textPage, next *textPosition) {
	out = []textPage{}
	for i, p := range pages {
		if p.Page < from.Page {
			continue
		}
		text := []rune(p.Text)
		start := 0
		if p.Page == from.Page {
			start = min(from.Offset, len(text))
		}
		text = text[start:]
		if limit > 0 && len(text) > limit {
			p.Text, p.Partial = string(text[:limit]), true
			return append(out, p), &textPosition{Page: p.Page, Offset: start + limit}
		}
		p.Text, p.Partial = string(text), start > 0
		out = append(out, p)
		if limit > 0 {
			limit -= len(text)
			if limit == 0 && i+1 < len(pages) {
				return out, &textPosition{Page: pages[i+1].Page}
			}
		}
	}
	return out, nil
}

// textQuery reads a ranged text read's page, offset and max_chars.
func textQuery(r *http.Request) (from textPosition, limit int, err error) {
	from.Page = 1
	for name, dst := range map[string]*int{"page": &from.Page, "offset": &from.Offset, "max_chars": &limit} {
		if v := r.URL.Query().Get(name); v != "" {
			if *dst, err = strconv.Atoi(v); err != nil {
				return from, 0, dms.Invalid(name + " must be a whole number")
			}
		}
	}
	if from.Page < 1 || from.Offset < 0 || limit < 0 {
		return from, 0, dms.Invalid("page must be at least 1, and offset and max_chars at least 0")
	}
	return from, limit, nil
}

// itemText returns the text read from the file version the caller may
// read: the current one, or blob_id. A ranged read continues with the
// text_id it began with, and fails when the text has changed since, so its
// parts never mix two files or two readings of one.
func (a *API) itemText(w http.ResponseWriter, r *http.Request) {
	from, limit, err := textQuery(r)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	b, text, err := a.dms(r).ItemText(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("blob_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	if began := r.URL.Query().Get("text_id"); began != "" && began != text.ID {
		a.problem(w, r, http.StatusPreconditionFailed, "text_changed", "The document's text changed since this read began; read it again from page 1.")
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
	pages, next := textRange(stored.Pages, from, limit)
	w.Header().Set("ETag", strconv.Quote(text.ID))
	respond(w, r, 200, map[string]any{
		"item_id": r.PathValue("id"), "blob_id": b.ID, "text_id": text.ID, "pages": pages, "next": next, "page_count": text.Pages, "chars": text.Chars,
		"language": text.Language, "ocr_pages": text.OcrPages, "skipped_pages": text.SkippedPages, "truncated": text.Truncated,
		"read_at": text.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}
