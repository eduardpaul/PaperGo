package workflow

import (
	"context"
	"errors"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"slices"
)

type launchIssue struct {
	code string
	err  error
}

func (e *launchIssue) Error() string  { return e.err.Error() }
func (e *launchIssue) Unwrap() error  { return e.err }
func issue(code, reason string) error { return &launchIssue{code: code, err: dms.Invalid(reason)} }

type Availability struct {
	CanRun     bool   `json:"can_run"`
	Code       string `json:"code"`
	Reason     string `json:"reason"`
	ManualMode string `json:"manual_mode"`
	Version    int    `json:"version"`
}

type Action struct {
	Workflow     *Workflow    `json:"workflow,omitempty"`
	BuiltIn      *BuiltIn     `json:"builtin,omitempty"`
	Availability Availability `json:"availability"`
}

func manualMode(def Definition) string {
	for _, tr := range def.Triggers {
		if tr.Type == TriggerManual {
			if tr.Selection == SelectionAll {
				return "selection"
			}
			if tr.CollectionID != "" || tr.Selection == SelectionPerItem {
				return "single"
			}
			return "standalone"
		}
	}
	return "none"
}

func (s *Service) CheckStart(ctx context.Context, subject, id string, in Start, checkInputs bool) (Availability, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Availability, error) {
		w, err := live(ctx, t, id)
		if err != nil {
			return Availability{}, err
		}
		if err = workspace(ctx, t, subject, w.WorkspaceID, "read"); err != nil {
			return Availability{}, err
		}
		v, err := current(ctx, t, w)
		if err != nil {
			return Availability{}, err
		}
		def, err := ParseDefinition(v.Definition)
		if err != nil {
			return Availability{}, err
		}
		a := Availability{CanRun: true, Code: "ready", Reason: "Ready to run once. Automatic settings stay unchanged.", ManualMode: manualMode(def), Version: v.Number}
		if _, err = s.planStart(ctx, t, subject, id, in, checkInputs); err != nil {
			a.CanRun = false
			a.Code = "unavailable"
			a.Reason = err.Error()
			var reason *launchIssue
			if errors.As(err, &reason) {
				a.Code = reason.code
			}
			if errors.Is(err, dms.ErrForbidden) || errors.Is(err, dms.ErrNotFound) {
				a.Code = "permission_required"
				a.Reason = "You or the workflow's execution author need permission for this run."
			}
			if w.State != workflows.StatePublished {
				a.Code = "not_published"
				a.Reason = "Publish this workflow before running it."
			}
			if !w.ManualEnabled {
				a.Code = "manual_not_allowed"
				a.Reason = "Manual runs are not allowed for this workflow."
			}
			if a.ManualMode == "none" {
				a.Code = "no_manual_entry"
				a.Reason = "This workflow has no manual entry point."
			}
		}
		return a, nil
	})
}

func (s *Service) Actions(ctx context.Context, subject, workspaceID string, in Start) ([]Action, error) {
	all, err := s.List(ctx, subject, workspaceID)
	if err != nil {
		return nil, err
	}
	out := []Action{}
	configured := map[string]bool{}
	for _, w := range all {
		if manualMode(w.Definition) == "none" {
			continue
		}
		a, err := s.CheckStart(ctx, subject, w.ID, in, false)
		if err != nil {
			return nil, err
		}
		// Standalone actions belong in the workspace catalog, not the file menu.
		if a.Code == "wrong_collection" || a.Code == "wrong_workspace" || len(in.ItemIDs) > 0 && a.ManualMode == "standalone" {
			continue
		}
		out = append(out, Action{Workflow: &w, Availability: a})
		if w.BuiltInKey != nil && a.CanRun {
			configured[*w.BuiltInKey] = true
		}
	}
	for _, b := range BuiltIns() {
		if b.ManualMode == "none" || configured[b.Key] || len(in.ItemIDs) > 0 && b.ManualMode == "standalone" {
			continue
		}
		if slices.ContainsFunc(out, func(a Action) bool {
			return a.Workflow != nil && a.Workflow.BuiltInKey != nil && *a.Workflow.BuiltInKey == b.Key
		}) {
			continue
		}
		out = append(out, Action{BuiltIn: b, Availability: Availability{Code: "needs_setup", Reason: "Ask a workspace manager to prepare this feature for manual use.", ManualMode: b.ManualMode}})
	}
	slices.SortStableFunc(out, func(a, b Action) int {
		if a.Availability.CanRun == b.Availability.CanRun {
			return 0
		}
		if a.Availability.CanRun {
			return -1
		}
		return 1
	})
	return out, nil
}

func (s *Service) manualReadiness(ctx context.Context, t *dms.Service, w *workflows.Workflow, author string, def Definition, items []string) error {
	if w.BuiltinKey != "" {
		if b := builtins[w.BuiltinKey]; b != nil && len(b.ManualMediaTypes) > 0 {
			for _, id := range items {
				file, err := t.CurrentFile(ctx, author, id)
				if err != nil {
					return err
				}
				if !slices.Contains(b.ManualMediaTypes, file.ContentType) {
					return issue("unsupported_file", "Requires a JPEG, PNG or WebP image for every selected file.")
				}
			}
		}
		if b := builtins[w.BuiltinKey]; b != nil && b.NeedsOCR && !s.OCR {
			return issue("needs_setup", "Needs setup: image text detection is unavailable.")
		}
	}
	return nil
}
