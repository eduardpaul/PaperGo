package workflow

import (
	"papergo/internal/dal/blobs"
	"papergo/internal/forms"
)

// Roles PaperGo fills with built-in workflows.
const (
	// RoleSearchIndex keeps items' and folders' names, fields, terms,
	// keywords and permissions in the search store.
	RoleSearchIndex = "search.index"
	// RoleSearchContent keeps the text of items' files searchable.
	RoleSearchContent = "search.content"
	// RoleFilePreview renders previews of items' files.
	RoleFilePreview = "file.preview"
)

func init() {
	RegisterRole(&Role{Key: RoleSearchIndex, Name: "Search index",
		Description: "Keeps the names, fields, terms, keywords and permissions of items and folders searchable. Turned off for a collection, searches inside it answer search_off, and global search keeps its entries as last indexed, still trimmed by current permissions."})
	RegisterRole(&Role{Key: RoleSearchContent, Name: "File content in search",
		Description: "Reads, chunks and publishes the text of files so search finds it, with the page. Turned off for a collection, its files' text leaves search as their content is replaced.",
		OutputSchema: forms.MustParse(`{"type": "object", "required": ["blob_id"], "properties": {
			"blob_id": {"type": "string", "description": "The file version indexed."},
			"text_id": {"type": "string", "description": "The text read."},
			"pages": {"type": "integer"}, "language": {"type": "string"},
			"generation": {"type": "string", "description": "The content published."},
			"chunks": {"type": "integer"}}}`)})
	RegisterRole(&Role{Key: RoleFilePreview, Name: "File previews", Description: "Generates lightweight first-page previews asynchronously."})

	images := []blobs.MediaType{blobs.MediaJPEG, blobs.MediaPNG, blobs.MediaWebP}
	approvers := `"approvers": {"type": "array", "title": "Approvers", "description": "Who reviews the new file; none means the workspace's owners.", "maxItems": 50, "items": {"type": "string"}, "x-papergo": {"kind": "people", "access": "read_draft"}}`
	RegisterBuiltIn(&BuiltIn{Key: "search.index", Name: "Index for search", Role: RoleSearchIndex, Default: true, System: true,
		Description:      "Keeps items and folders searchable: on every change, permission change and term relabel, re-reads the entry and writes it to the search index.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "search.content", Name: "Index file content", Role: RoleSearchContent, Default: true, System: true,
		Description: "When a PDF or an image is added, reads its text (OCR for scans and photos when configured), splits it into chunks, embeds them for semantic search when an embedding model is configured, and makes it searchable with its pages. A reindex with content runs it again for files without content.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"ocr": {"type": "string", "title": "OCR", "enum": ["auto", "always", "never"], "default": "auto"},
			"max_pages": {"type": "integer", "title": "Maximum pages", "minimum": 1, "maximum": 2000, "default": 500},
			"chunker": {"type": "string", "title": "Chunker", "enum": ["window", "pages", "paragraphs", "headings", "whole"], "default": "window"},
			"max_chars": {"type": "integer", "title": "Chunk size", "minimum": 100, "maximum": 20000, "default": 1200},
			"overlap": {"type": "integer", "title": "Overlap", "minimum": 0, "maximum": 5000, "default": 150},
			"max_chunks": {"type": "integer", "title": "Maximum chunks", "minimum": 1, "maximum": 2000, "default": 400},
			"embed": {"type": "string", "title": "Embedding", "enum": ["immediate", "later"], "default": "immediate", "description": "When an embedding model is configured: embed the chunks before publishing, or leave them to the sweep."}
		}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "documents.preview", Name: "Generate file previews", Role: RoleFilePreview, Default: true, System: true,
		Description:      "Generates bounded WebP previews when files are added or replaced. Also runs on demand.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "items.expire", Name: "Unpublish expired items", PerCollection: true,
		Description: "Every day, unpublishes the published items whose date field is before today.",
		ParametersSchema: forms.MustParse(`{"type": "object", "required": ["field"], "properties": {
			"field": {"type": "string", "title": "Expiry field", "description": "Key of an indexed date field holding the expiry date."},
			"cron": {"type": "string", "title": "Schedule", "description": "When to check, as a cron expression.", "default": "0 1 * * *"},
			"time_zone": {"type": "string", "title": "Time zone", "description": "Time zone of the schedule.", "default": "UTC"}
		}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "documents.optimize", Name: "Optimize image storage", PerCollection: true, NeedsOCR: true, ManualMediaTypes: images,
		Description: "When an image is added, proposes a smaller WebP copy whose smallest text keeps a readable height, and asks the owners (or the approvers) to compare both before it replaces the file. A newer file cancels the waiting review. Also runs on demand.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text keeps.", "minimum": 4, "maximum": 200, "default": 12},
			"min_confidence": {"type": "number", "title": "Minimum confidence", "description": "Detector score a text line needs, 0 to 100.", "minimum": 0, "maximum": 100, "default": 50},
			"percentile": {"type": "number", "title": "Height percentile", "description": "Percentile of the text heights taken as the smallest text.", "minimum": 0, "maximum": 100, "default": 5},
			"max_analysis_dimension": {"type": "integer", "title": "Analysis size", "description": "Longest side in pixels of the copy the detector analyzes.", "minimum": 256, "maximum": 2600, "default": 2600},
			"minimum_scale": {"type": "number", "title": "Minimum scale", "description": "How far an image may shrink.", "minimum": 0.01, "maximum": 1, "default": 0.05},
			"quality": {"type": "integer", "title": "WebP quality", "minimum": 1, "maximum": 100, "default": 80},
			` + approvers + `
		}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "documents.photos_to_pdf", Name: "Photos to PDF", PerCollection: true, NeedsOCR: true, ManualMediaTypes: images,
		Description: "Combines selected photos, in the chosen order, into one searchable PDF. After the owners (or the approvers) review it, the primary item gets the PDF as its new file, keeping its metadata and file history, and the other photos go to the recycle bin. Started on a selection.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text of each page keeps; pages never grow.", "minimum": 8, "maximum": 200, "default": 32},
			"quality": {"type": "integer", "title": "JPEG quality", "minimum": 1, "maximum": 100, "default": 80},
			` + approvers + `
		}}`)})
}
