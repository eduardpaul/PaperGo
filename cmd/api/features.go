package main

import (
	"papergo/internal/aiops"
	"papergo/internal/dms"
	"papergo/internal/documentfiles"
	"papergo/internal/documentpreview"
	"papergo/internal/documenttext"
	"papergo/internal/httpapi"
	"papergo/internal/searchcontent"
)

// features are the compiled feature modules: their operations, routes,
// workers and maintenance.
type features struct {
	text    *documenttext.Reader
	preview *documentpreview.Renderer
	search  *searchcontent.Indexer
	ai      *aiops.Assistant
	files   *documentfiles.Maker
}

// register makes s run the features' operations.
func (f features) register(s *dms.Service) error {
	for _, register := range []func(*dms.Service) error{f.text.Register, f.preview.Register, f.search.Register, f.ai.Register, f.files.Register} {
		if err := register(s); err != nil {
			return err
		}
	}
	return nil
}

// modules are the feature modules whose routes the API serves.
func (f features) modules() []httpapi.Module {
	return []httpapi.Module{f.text.Module(), f.preview.Module(), f.search.Module(), f.ai.Module(), f.files.Module()}
}
