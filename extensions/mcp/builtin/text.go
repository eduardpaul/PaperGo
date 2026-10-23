package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

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
			"It returns up to max_chars characters from page (default 1), starting offset characters into it; continue with next_page, " +
			"next_offset and the first part's text_id while next_page is not null, whatever the file's size. A page cut short is marked partial. " +
			"A continuation fails with version_conflict when the document's text changed since the first part, as when its file was replaced: " +
			"read it again from page 1. Files whose text PaperGo has not read yet fail with not_found.",
		InputSchema: object([]string{"item_id"},
			property("item_id", idProperty("The document: an item of a library.")),
			property("page", `{"type": "integer", "minimum": 1, "default": 1, "description": "The first page to return."}`),
			property("offset", `{"type": "integer", "minimum": 0, "default": 0, "description": "Characters to skip into the first page, as next_offset gives them."}`),
			property("max_chars", `{"type": "integer", "minimum": 1000, "maximum": 100000, "default": 20000, "description": "At most this many characters of text."}`),
			property("text_id", `{"type": "string", "description": "When continuing, the text_id the first part returned."}`)),
		OutputSchema: json.RawMessage(`{"type": "object", "required": ["item_id", "text_id", "page_count", "pages", "next_page", "next_offset", "language", "ocr_pages", "skipped_pages", "truncated"], "properties": {
			"item_id": {"type": "string"},
			"text_id": {"type": "string", "description": "The text these pages come from; pass it back when continuing."},
			"page_count": {"type": "integer", "description": "Pages in the file."},
			"pages": {"type": "array", "items": {"type": "object", "required": ["page", "text"], "properties": {
				"page": {"type": "integer"}, "text": {"type": "string"},
				"ocr": {"type": "boolean", "description": "The text was recognized from the page's image."},
				"partial": {"type": "boolean", "description": "Only part of the page's text is here."}}}},
			"next_page": {"type": ["integer", "null"], "description": "The page to continue from; null after the end."},
			"next_offset": {"type": "integer", "description": "Characters into next_page to continue from."},
			"language": {"type": "string"},
			"ocr_pages": {"type": "integer"},
			"skipped_pages": {"type": "integer", "description": "Pages without a text layer that were not recognized."},
			"truncated": {"type": "boolean", "description": "PaperGo stored only the first part of a very long file's text."}}}`),
		Scopes:      []string{scopeItemRead, scopeDocumentRead},
		Annotations: mcp.Annotations{ReadOnly: true, Idempotent: true},
		Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (json.RawMessage, error) {
			in := struct {
				ItemID   string `json:"item_id"`
				Page     int    `json:"page"`
				Offset   int    `json:"offset"`
				MaxChars int    `json:"max_chars"`
				TextID   string `json:"text_id"`
			}{Page: 1, MaxChars: defaultTextChars}
			if err := decode(args, &in); err != nil {
				return nil, err
			}
			// PaperGo cuts the range, so only the asked-for part travels.
			query := url.Values{"page": {fmt.Sprint(in.Page)}, "offset": {fmt.Sprint(in.Offset)}, "max_chars": {fmt.Sprint(in.MaxChars)}}
			if in.TextID != "" {
				query.Set("text_id", in.TextID)
			}
			var text struct {
				ItemID string `json:"item_id"`
				TextID string `json:"text_id"`
				Pages  []struct {
					Page    int    `json:"page"`
					Text    string `json:"text"`
					OCR     bool   `json:"ocr,omitempty"`
					Partial bool   `json:"partial,omitempty"`
				} `json:"pages"`
				Next *struct {
					Page   int `json:"page"`
					Offset int `json:"offset"`
				} `json:"next"`
				PageCount    int    `json:"page_count"`
				Language     string `json:"language"`
				OCRPages     int    `json:"ocr_pages"`
				SkippedPages int    `json:"skipped_pages"`
				Truncated    bool   `json:"truncated"`
			}
			if err := get(ctx, c, "/v1/items/"+url.PathEscape(in.ItemID)+"/text", query, &text); err != nil {
				return nil, err
			}
			out := struct {
				ItemID       string `json:"item_id"`
				TextID       string `json:"text_id"`
				PageCount    int    `json:"page_count"`
				Pages        any    `json:"pages"`
				NextPage     *int   `json:"next_page"`
				NextOffset   int    `json:"next_offset"`
				Language     string `json:"language"`
				OCRPages     int    `json:"ocr_pages"`
				SkippedPages int    `json:"skipped_pages"`
				Truncated    bool   `json:"truncated"`
			}{ItemID: text.ItemID, TextID: text.TextID, PageCount: text.PageCount, Pages: text.Pages, Language: text.Language, OCRPages: text.OCRPages,
				SkippedPages: text.SkippedPages, Truncated: text.Truncated}
			if text.Pages == nil {
				out.Pages = []struct{}{}
			}
			if text.Next != nil {
				out.NextPage, out.NextOffset = &text.Next.Page, text.Next.Offset
			}
			return encode(out)
		},
	}
}
