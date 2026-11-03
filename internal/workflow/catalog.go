package workflow

import (
	"papergo/internal/dal/blobs"
	"papergo/internal/forms"
)

func init() {
	images := []blobs.MediaType{blobs.MediaJPEG, blobs.MediaPNG, blobs.MediaWebP}
	contentType := `"content_type": {"type": "string", "title": "Content type", "description": "Only items of this content type or its subtypes; all items when empty.", "x-papergo": {"kind": "content_type"}}`
	approvers := `"approvers": {"type": "array", "title": "Approvers", "description": "Who reviews the new file; none means the workspace's owners.", "maxItems": 50, "items": {"type": "string"}, "x-papergo": {"kind": "people", "access": "read_draft"}}`
	RegisterBuiltIn(&BuiltIn{Key: "items.expire", Name: "Unpublish expired items", PerCollection: true,
		Description: "Every day, unpublishes the published items whose date field is before today.",
		ParametersSchema: forms.MustParse(`{"type": "object", "required": ["field"], "properties": {
			"field": {"type": "string", "title": "Expiry field", "description": "Key of an indexed date field holding the expiry date."},
			"cron": {"type": "string", "title": "Schedule", "description": "When to check, as a cron expression.", "default": "0 1 * * *"},
			"time_zone": {"type": "string", "title": "Time zone", "description": "Time zone of the schedule.", "default": "UTC"},
			` + contentType + `
		}}`)})
	RegisterBuiltIn(&BuiltIn{Key: "documents.photos_to_pdf", Name: "Photos to PDF", PerCollection: true, NeedsOCR: true, ManualMediaTypes: images,
		Description: "Combines selected photos, in the chosen order, into one searchable PDF. After the owners (or the approvers) review it, the primary item gets the PDF as its new file, keeping its metadata and file history, and the other photos go to the recycle bin. Started on a selection.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text of each page keeps; pages never grow.", "minimum": 8, "maximum": 200, "default": 32},
			"quality": {"type": "integer", "title": "JPEG quality", "minimum": 1, "maximum": 100, "default": 80},
			` + approvers + `
		}}`)})
}
