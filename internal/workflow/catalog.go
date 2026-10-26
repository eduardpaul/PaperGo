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
	RegisterBuiltIn(&BuiltIn{Key: "documents.optimize", Name: "Optimize image storage", PerCollection: true, NeedsOCR: true, ManualMediaTypes: images,
		Description: "When an image is added, proposes a smaller WebP copy whose smallest text keeps a readable height, and asks the owners (or the approvers) to compare both before it replaces the file. A newer file cancels the waiting review. Also runs on demand.",
		ParametersSchema: forms.MustParse(`{"type": "object", "properties": {
			"target_height": {"type": "number", "title": "Smallest text height", "description": "Height in pixels the smallest text keeps.", "minimum": 4, "maximum": 200, "default": 12},
			"min_confidence": {"type": "number", "title": "Minimum confidence", "description": "Detector score a text line needs, 0 to 100.", "minimum": 0, "maximum": 100, "default": 50},
			"percentile": {"type": "number", "title": "Height percentile", "description": "Percentile of the text heights taken as the smallest text.", "minimum": 0, "maximum": 100, "default": 5},
			"max_analysis_dimension": {"type": "integer", "title": "Analysis size", "description": "Longest side in pixels of the copy the detector analyzes.", "minimum": 256, "maximum": 2600, "default": 2600},
			"minimum_scale": {"type": "number", "title": "Minimum scale", "description": "How far an image may shrink.", "minimum": 0.01, "maximum": 1, "default": 0.05},
			"quality": {"type": "integer", "title": "WebP quality", "minimum": 1, "maximum": 100, "default": 80},
			` + contentType + `,
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
