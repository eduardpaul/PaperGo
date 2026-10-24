package httpapi

import (
	"papergo/internal/doctext"
	previewer "papergo/internal/preview"
	"papergo/internal/storage"
)

// fileType describes a kind of file to the web app: its name, icon family,
// extensions, and whether PaperGo renders previews and reads text of it.
type fileType struct {
	MediaType  string   `json:"media_type"`
	Label      string   `json:"label"`
	Family     string   `json:"family"`
	Extensions []string `json:"extensions"`
	Preview    bool     `json:"preview"`
	Text       bool     `json:"text"`
}

func fileTypes() []fileType {
	registered := storage.FileTypes()
	out := make([]fileType, 0, len(registered))
	for _, t := range registered {
		extensions := t.Extensions
		if extensions == nil {
			extensions = []string{}
		}
		out = append(out, fileType{MediaType: t.Media, Label: t.Label, Family: t.Family, Extensions: extensions, Preview: previewer.Supported(t.Media), Text: doctext.Supported(t.Media)})
	}
	return out
}
