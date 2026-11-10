package solutions

import (
	"context"
	"errors"
	"fmt"

	"papergo/internal/apps"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// prunable says whether objects of a kind are deleted with their solution:
// what it runs and shows. What holds or describes stored items stays.
func prunable(kind string) bool {
	return kind == KindWorkflow || kind == KindApp || kind == KindView
}

// discard deletes a workflow, an app or a view of a solution. One that is
// already gone is no error.
func discard(ctx context.Context, t *dms.Service, subject string, o Object) error {
	var err error
	switch o.Kind {
	case KindWorkflow:
		svc := &workflow.Service{DMS: t}
		w, e := svc.Get(ctx, subject, o.ID)
		if e != nil {
			err = e
			break
		}
		err = svc.Delete(ctx, subject, o.ID, w.Version)
	case KindApp:
		svc := &apps.Service{DMS: t}
		a, e := svc.Get(ctx, subject, o.ID)
		if e != nil {
			err = e
			break
		}
		err = svc.Delete(ctx, subject, o.ID, a.Version)
	case KindView:
		v, e := t.View(ctx, subject, o.ID)
		if e != nil {
			err = e
			break
		}
		err = t.DeleteView(ctx, subject, o.ID, v.Version)
	}
	if errors.Is(err, dms.ErrNotFound) {
		return nil
	}
	return err
}

// Remove uninstalls a solution: its workflows, apps and views are deleted,
// and its collections, content types and fields stay with everything they
// hold. A solution another installation uses is not removed.
func (s *Service) Remove(ctx context.Context, subject, workspaceID, key string) (Result, error) {
	var out Result
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		workspace, err := t.Authorize(ctx, subject, workspaceID, "manage")
		if err != nil {
			return err
		}
		if err := t.RequireScope(ctx, subject, Scope); err != nil {
			return err
		}
		r, err := readInstalled(ctx, t, workspaceID)
		if err != nil {
			return err
		}
		in, ok := r.Installations[key]
		if !ok {
			return dms.ErrNotFound
		}
		out = Result{Solution: in.Key, Name: in.Name, Version: in.Version, From: in.Version, Changes: []Change{}, Refusals: []Refusal{}, Customized: []string{}, Warnings: []string{}, Slots: []Slot{}, Settings: map[string]any{}, Languages: []string{}}
		for _, other := range r.keys() {
			if other == key {
				continue
			}
			for _, slot := range r.Installations[other].Manifest.Collections {
				if slot.Use != nil && slot.Use.Solution == key {
					return dms.Invalid(fmt.Sprintf("the solution %s uses the collection %q of %s; remove %s first", other, slot.Use.Collection, key, other))
				}
			}
		}
		for _, o := range in.Objects {
			if !prunable(o.Kind) || o.Role != RoleOwns {
				if o.Kind == KindCollection || o.Kind == KindContentType || o.Kind == KindField {
					out.Changes = append(out.Changes, Change{"keep", o.Kind, o.Key, "with what it holds"})
				}
				continue
			}
			if err := discard(ctx, t, subject, o); err != nil {
				return failed(o.Kind, o.Key, err)
			}
			out.Changes = append(out.Changes, Change{"delete", o.Kind, o.Key, ""})
		}
		delete(r.Installations, key)
		if err := writeInstalled(ctx, t, workspaceID, subject, r); err != nil {
			return err
		}
		out.Applied = true
		return t.Record(ctx, subject, "solution.remove", workspace, map[string]any{"solution": key, "version": in.Version})
	})
	return out, err
}
