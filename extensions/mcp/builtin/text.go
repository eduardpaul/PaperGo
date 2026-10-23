package builtin

import (
	"context"
	"encoding/json"
	"net/url"
	"unicode/utf8"

	"papergo/extensions/mcp"
)

// Bounds of the text one get_item_text call returns, in characters.
const (
	defaultTextChars = 20000
	maxTextChars     = 100000
)

func getItemText() mcp.Tool {
	return mcp.Tool{
		Name:  "get_item_text",
		Title: "Read a document's text",
		Description: "Reads the text PaperGo extracted from a document's file, page by page, so you can answer questions about what it says. " +
			"Pages start at page (default 1) and stop before max_chars characters; continue with next_page while it is not null. " +
			"A page longer than max_chars is cut, and partial marks it. Files whose text PaperGo has not read yet fail with not_found.",
		InputSchema: object([]string{"item_id"},
			property("item_id", idProperty("The document: an item of a library.")),
			property("page", `{"type": "integer", "minimum": 1, "default": 1, "description": "The first page to return."}`),
			property("max_chars", `{"type": "integer", "minimum": 1000, "maximum": 100000, "default": 20000, "description": "At most this many characters of text."}`)),
		OutputSchema: json.RawMessage(`{"type": "object", "required": ["item_id", "page_count", "pages", "next_page", "language", "ocr_pages", "skipped_pages", "truncated"], "properties": {
			"item_id": {"type": "string"},
			"page_count": {"type": "integer", "description": "Pages in the file."},
			"pages": {"type": "array", "items": {"type": "object", "required": ["page", "text"], "properties": {
				"page": {"type": "integer"}, "text": {"type": "string"},
				"ocr": {"type": "boolean", "description": "The text was recognized from the page's image."},
				"partial": {"type": "boolean", "description": "The page's text was cut at max_chars."}}}},
			"next_page": {"type": ["integer", "null"], "description": "The page to continue from; null after the last one."},
			"language": {"type": "string"},
			"ocr_pages": {"type": "integer"},
			"skipped_pages": {"type": "integer", "description": "Pages without a text layer that were not recognized."},
			"truncated": {"type": "boolean", "description": "PaperGo stored only the first part of a very long file's text."}}}`),
		Scopes:      []string{scopeListRead, scopeDocumentRead},
		Annotations: mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			var in struct {
				ItemID   string `json:"item_id"`
				Page     *int   `json:"page"`
				MaxChars *int   `json:"max_chars"`
			}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			first, budget := 1, defaultTextChars
			if in.Page != nil {
				first = *in.Page
			}
			if in.MaxChars != nil {
				budget = *in.MaxChars
			}
			type page struct {
				Page    int    `json:"page"`
				Text    string `json:"text"`
				OCR     bool   `json:"ocr,omitempty"`
				Partial bool   `json:"partial,omitempty"`
			}
			var text struct {
				ItemID       string `json:"item_id"`
				Pages        []page `json:"pages"`
				PageCount    int    `json:"page_count"`
				Language     string `json:"language"`
				OCRPages     int    `json:"ocr_pages"`
				SkippedPages int    `json:"skipped_pages"`
				Truncated    bool   `json:"truncated"`
			}
			if err := get(ctx, c, "/v1/items/"+url.PathEscape(in.ItemID)+"/text", nil, &text); err != nil {
				return nil, err
			}
			out := struct {
				ItemID       string `json:"item_id"`
				PageCount    int    `json:"page_count"`
				Pages        []page `json:"pages"`
				NextPage     *int   `json:"next_page"`
				Language     string `json:"language"`
				OCRPages     int    `json:"ocr_pages"`
				SkippedPages int    `json:"skipped_pages"`
				Truncated    bool   `json:"truncated"`
			}{ItemID: text.ItemID, PageCount: text.PageCount, Pages: []page{}, Language: text.Language, OCRPages: text.OCRPages,
				SkippedPages: text.SkippedPages, Truncated: text.Truncated}
			for i, p := range text.Pages {
				if p.Page < first {
					continue
				}
				n := utf8.RuneCountInString(p.Text)
				if n > budget && len(out.Pages) > 0 {
					next := p.Page
					out.NextPage = &next
					break
				}
				if n > budget {
					p.Text, p.Partial = cut(p.Text, budget), true
				}
				out.Pages = append(out.Pages, p)
				budget -= min(n, budget)
				if budget == 0 && i+1 < len(text.Pages) {
					next := text.Pages[i+1].Page
					out.NextPage = &next
					break
				}
			}
			return encode(out)
		},
	}
}

// cut keeps the first n characters of s.
func cut(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
