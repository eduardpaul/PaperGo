package solutions

import (
	"context"
	"fmt"
	"sort"

	"papergo/internal/apps"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// Summary is an installed solution in a list.
type Summary struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Paused    bool   `json:"paused"`
	AppliedBy string `json:"applied_by"`
	AppliedAt string `json:"applied_at"`
}

// Status is whether an installed solution still has what it needs.
type Status struct {
	Solution string `json:"solution"`
	Version  string `json:"version"`
	OK       bool   `json:"ok"`
	// Problems say what is missing or no longer as the solution needs it.
	Problems []string `json:"problems"`
}

// workspace reads the solutions of a workspace its readers may see.
func (s *Service) workspace(ctx context.Context, t *dms.Service, subject, workspaceID string) (installed, error) {
	if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
		return installed{}, err
	}
	return readInstalled(ctx, t, workspaceID)
}

// List returns the solutions installed in a workspace, by key.
func (s *Service) List(ctx context.Context, subject, workspaceID string) ([]Summary, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]Summary, error) {
		r, err := s.workspace(ctx, t, subject, workspaceID)
		if err != nil {
			return nil, err
		}
		out := []Summary{}
		for _, key := range r.keys() {
			in := r.Installations[key]
			out = append(out, Summary{in.Key, in.Name, in.Version, in.Paused, in.AppliedBy, in.AppliedAt.Format("2006-01-02T15:04:05Z")})
		}
		return out, nil
	})
}

// Get returns one installed solution: its settings, bindings and objects.
func (s *Service) Get(ctx context.Context, subject, workspaceID, key string) (Installation, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Installation, error) {
		r, err := s.workspace(ctx, t, subject, workspaceID)
		if err != nil {
			return Installation{}, err
		}
		in, ok := r.Installations[key]
		if !ok {
			return Installation{}, dms.ErrNotFound
		}
		return in, nil
	})
}

// Status checks an installed solution against what it declared when it was
// applied: its collections with their content types and fields, what it
// expects of others' collections, and its workflows and apps.
func (s *Service) Status(ctx context.Context, subject, workspaceID, key string) (Status, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (Status, error) {
		r, err := s.workspace(ctx, t, subject, workspaceID)
		if err != nil {
			return Status{}, err
		}
		in, ok := r.Installations[key]
		if !ok {
			return Status{}, dms.ErrNotFound
		}
		out := Status{Solution: in.Key, Version: in.Version, Problems: []string{}}
		say := func(format string, args ...any) { out.Problems = append(out.Problems, fmt.Sprintf(format, args...)) }
		for _, slot := range in.Manifest.Collections {
			o := in.object(KindCollection, slot.Key)
			if o == nil {
				say("%s: the collection was never installed", slot.Key)
				continue
			}
			c, err := t.Get(ctx, subject, o.ID)
			if err != nil {
				say("%s: the %s cannot be read; it may have been deleted", slot.Key, slot.Kind)
				continue
			}
			if string(c.Kind) != slot.Kind {
				say("%s: %q is a %s, not a %s", slot.Key, c.Name, c.Kind, slot.Kind)
				continue
			}
			contract := slot
			if slot.Owned() {
				// What the solution's own collection must have is what it
				// declares for it.
				contract.Expects = map[string][]string{}
				for _, typ := range slot.ContentTypes {
					for _, declared := range in.Manifest.ContentTypes {
						if declared.Key == typ {
							contract.Expects[typ] = declared.Fields
						}
					}
				}
			} else {
				contract.Expects = map[string][]string{}
				for typ, fields := range slot.Expects {
					contract.Expects[typ] = append(append([]string{}, fields...), slot.Extends[typ]...)
				}
			}
			problems, _, err := expected(ctx, t, subject, contract, c.ID)
			if err != nil {
				return Status{}, err
			}
			for _, p := range problems {
				say("%s (%s): %s", slot.Key, c.Name, p)
			}
		}
		workflows, applications := &workflow.Service{DMS: t}, &apps.Service{DMS: t}
		for _, o := range in.Objects {
			if o.Retired {
				continue // no longer part of the solution: it needs nothing of it
			}
			switch o.Kind {
			case KindWorkflow:
				w, err := workflows.Get(ctx, subject, o.ID)
				declared := in.Components[KindWorkflow+":"+o.Key].AutomaticEnabled
				if err != nil {
					say("workflow %s cannot be read; it may have been deleted", o.Key)
				} else if w.State != "published" {
					say("workflow %s is %s, so it does not run", o.Key, w.State)
				} else if declared != nil && *declared && !w.AutomaticEnabled && !in.Paused {
					say("workflow %s no longer starts on its own: its automatic starts were turned off", o.Key)
				}
			case KindApp:
				a, err := applications.Get(ctx, subject, o.ID)
				if err != nil {
					say("app %s cannot be read; it may have been deleted", o.Key)
				} else if a.State != "published" {
					say("app %s is a %s, so people do not see it", o.Key, a.State)
				}
			}
		}
		required, optional := s.lacks(in.Manifest)
		for _, name := range required {
			say("needs %s, which this PaperGo does not provide", name)
		}
		for _, name := range optional {
			say("works in part: this PaperGo does not provide %s", name)
		}
		sort.Strings(out.Problems)
		out.OK = len(out.Problems) == 0
		return out, nil
	})
}
