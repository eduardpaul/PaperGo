package main

import (
	"papergo/internal/aiops"
	"papergo/internal/dms"
	"papergo/internal/documentfiles"
	"papergo/internal/documentpreview"
	"papergo/internal/documentsearch"
	"papergo/internal/documenttext"
	"papergo/internal/httpapi"
)

// features are the compiled feature modules: their operations, routes,
// workers and maintenance. Search is nil when no search store is
// configured.
type features struct {
	text    *documenttext.Reader
	preview *documentpreview.Renderer
	search  *documentsearch.Search
	ai      *aiops.Assistant
	files   *documentfiles.Maker
}

// register makes s run the features' operations.
func (f features) register(s *dms.Service) error {
	registers := []func(*dms.Service) error{f.text.Register, f.preview.Register, f.ai.Register, f.files.Register}
	if f.search != nil {
		registers = append(registers, f.search.Register)
	}
	for _, register := range registers {
		if err := register(s); err != nil {
			return err
		}
	}
	return nil
}

// modules are the feature modules whose routes the API serves.
func (f features) modules() []httpapi.Module {
	modules := []httpapi.Module{f.text.Module(), f.preview.Module(), f.ai.Module(), f.files.Module()}
	if f.search != nil {
		modules = append(modules, f.search.Module())
	}
	return modules
}
