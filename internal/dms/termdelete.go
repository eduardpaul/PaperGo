package dms

import (
	"context"
	"fmt"
	"strings"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
)

// EventTermDeleted is raised when a term is deleted; data has term_id,
// term_set_id, parent_id (empty at the root), name and merged_term_ids, the
// terms merged into it that went with it.
const EventTermDeleted = "term.deleted"

// TermUsage counts what names a term or a term merged into it.
type TermUsage struct {
	// ChildTerms are the term's children.
	ChildTerms int `json:"child_terms"`
	// Items are the live items and folders whose current or published
	// content holds the term, as a keyword or in a field.
	Items int `json:"items"`
	// DeletedItems are the items and folders in a recycle bin that hold it.
	DeletedItems int `json:"deleted_items"`
	// Workspaces and Collections carry it as a keyword.
	Workspaces  int `json:"workspaces"`
	Collections int `json:"collections"`
	// Fields give it as their default.
	Fields int `json:"fields"`
	// ContentTypes give it as a field's default or test for it in a rule.
	ContentTypes int `json:"content_types"`
	// Templates are collection templates whose fields give it as a default.
	Templates int `json:"templates"`
	// Views are list views that filter on it.
	Views int `json:"views"`
	// SmartFolders select or filter on it.
	SmartFolders int `json:"smart_folders"`
	// Workflows name it in a trigger or a form of their current version.
	Workflows int `json:"workflows"`
	// Relationships have the type it names.
	Relationships int `json:"relationships"`
}

// counts lists the uses in the order people read them.
func (u TermUsage) counts() []struct {
	n          int
	one, other string
} {
	return []struct {
		n          int
		one, other string
	}{
		{u.Items, "item", "items"}, {u.DeletedItems, "item in a recycle bin", "items in a recycle bin"},
		{u.ChildTerms, "child term", "child terms"}, {u.Workspaces, "workspace", "workspaces"}, {u.Collections, "collection", "collections"},
		{u.Fields, "field default", "field defaults"}, {u.ContentTypes, "content type", "content types"}, {u.Templates, "collection template", "collection templates"},
		{u.Views, "view", "views"}, {u.SmartFolders, "smart folder", "smart folders"}, {u.Workflows, "workflow", "workflows"},
		{u.Relationships, "relationship", "relationships"},
	}
}

// Any reports whether anything names the term.
func (u TermUsage) Any() bool { return u != TermUsage{} }

// TermInUse refuses to delete a term that something still names.
type TermInUse struct{ Usage TermUsage }

func (e *TermInUse) Error() string {
	parts := []string{}
	for _, c := range e.Usage.counts() {
		switch {
		case c.n == 1:
			parts = append(parts, "1 "+c.one)
		case c.n > 1:
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.other))
		}
	}
	return "the term is in use by " + strings.Join(parts, ", ") + "; remove it there first"
}

func (e *TermInUse) Unwrap() error { return ErrConflict }

// DeleteTerm deletes a term that nothing names, with the terms merged into
// it, the keyword links of both and the relationship type it names. Its name
// becomes free for another term. A term is in use, and stays, while it has
// children or while an item (also one in a recycle bin), a workspace or
// collection keyword, a field default, a content type, a collection
// template, a list view, a smart folder, a workflow or a relationship names
// it or a term merged into it: *TermInUse counts them. Retained revisions
// that are no item's current content may keep the ID; restoring one leaves
// the term out.
func (s *Service) DeleteTerm(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		if _, _, e := t.termForChange(ctx, subject, id, version); e != nil {
			return e
		}
		var merged []string
		removed, e := t.DAL.Taxonomy().DeleteTerm(ctx, id, version, func(ids []string, children int) error {
			held, e := t.DAL.Content().TermUsage(ctx, ids)
			if e != nil {
				return e
			}
			usage := TermUsage{ChildTerms: children, Items: held.Items, DeletedItems: held.DeletedItems, Workspaces: held.Workspaces, Collections: held.Collections,
				Fields: held.Fields, ContentTypes: held.ContentTypes, Templates: held.Templates, Views: held.ListViews, SmartFolders: held.SmartFolders,
				Workflows: held.Workflows, Relationships: held.Relationships}
			if usage.Any() {
				return &TermInUse{Usage: usage}
			}
			merged = ids[1:]
			return nil
		})
		if e != nil {
			return taxonomyError(e)
		}
		workspaces, e := t.DAL.Resources().IDs(ctx, resources.Filter{Kinds: []refs.ResourceKind{refs.KindWorkspace}, State: resources.Live})
		if e != nil {
			return e
		}
		for _, workspaceID := range workspaces {
			t.emit(ctx, EventTermDeleted, subject, workspaceID, nil, "", map[string]any{
				"term_id": id, "term_set_id": removed.SetID, "parent_id": removed.ParentID, "name": removed.Name, "merged_term_ids": merged,
			})
		}
		return t.auditTaxonomy(ctx, subject, "term.delete", map[string]any{"term_id": id, "term_set_id": removed.SetID})
	})
}

// dropDeletedTerms removes the IDs of terms that no longer exist from
// keywords and from the term and keywords fields of values, which a
// retained revision wrote: a field left without a term is cleared.
func (s *Service) dropDeletedTerms(ctx context.Context, defs []*catalog.FieldDefinition, keywords []string, values map[string]any) ([]string, error) {
	held := append([]string{}, keywords...)
	fields := []*catalog.FieldDefinition{}
	for _, d := range defs {
		if d.Type != catalog.FieldTerm && d.Type != catalog.FieldKeywords {
			continue
		}
		fields = append(fields, d)
		list, _ := values[d.Key].([]any)
		if !d.Options.Multiple {
			list = []any{values[d.Key]}
		}
		for _, v := range list {
			if id, ok := v.(string); ok {
				held = append(held, id)
			}
		}
	}
	if len(held) == 0 {
		return keywords, nil
	}
	rows, err := s.DAL.Taxonomy().Terms(ctx, taxonomy.TermFilter{IDs: held})
	if err != nil {
		return nil, err
	}
	exists := map[string]bool{}
	for _, t := range rows {
		exists[t.ID] = true
	}
	kept := []string{}
	for _, id := range keywords {
		if exists[id] {
			kept = append(kept, id)
		}
	}
	for _, d := range fields {
		if !d.Options.Multiple {
			if id, ok := values[d.Key].(string); ok && !exists[id] {
				values[d.Key] = nil
			}
			continue
		}
		list, ok := values[d.Key].([]any)
		if !ok {
			continue
		}
		left := []any{}
		for _, v := range list {
			if id, ok := v.(string); !ok || exists[id] {
				left = append(left, v)
			}
		}
		switch {
		case len(left) == len(list):
		case len(left) == 0:
			values[d.Key] = nil
		default:
			values[d.Key] = left
		}
	}
	return kept, nil
}
