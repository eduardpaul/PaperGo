package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"strings"
)

type Page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Browse struct {
	ParentID    string
	WorkspaceID string
	Search      string
	Tag         string
	After       string
	Limit       int
	Surface     string
	FilterField string
	FilterOp    string
	FilterValue string
}

func pageSize(limit int) int {
	if limit < 1 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func (s *Service) Browse(ctx context.Context, subject string, in Browse) (Page[*ent.Resource], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*ent.Resource], error) { return t.Browse(ctx, subject, in) })
	}
	if in.Surface == "" {
		in.Surface = "auto"
	}
	if in.Surface != "auto" && in.Surface != "head" && in.Surface != "published" {
		return Page[*ent.Resource]{}, invalid("surface must be auto, head or published")
	}
	if len(in.Search) > 256 {
		return Page[*ent.Resource]{}, invalid("search is limited to 256 bytes")
	}
	q := s.Client.Resource.Query()
	if in.ParentID != "" {
		if _, err := s.authorize(ctx, subject, in.ParentID, "read"); err != nil {
			return Page[*ent.Resource]{}, err
		}
		q.Where(resource.ParentIDEQ(in.ParentID))
	} else if in.WorkspaceID != "" {
		w, err := s.authorize(ctx, subject, in.WorkspaceID, "read")
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		if w.Kind != resource.KindWorkspace {
			return Page[*ent.Resource]{}, invalid("workspace_id must reference a workspace")
		}
		q.Where(resource.WorkspaceIDEQ(in.WorkspaceID))
	} else {
		if in.Search != "" || in.Tag != "" {
			return Page[*ent.Resource]{}, invalid("search and tags require workspace_id or parent_id")
		}
		q.Where(resource.KindEQ(resource.KindWorkspace))
	}
	if in.After != "" {
		q.Where(resource.IDGT(in.After))
	}
	action := "read"
	if in.Surface == "head" {
		action = "read_draft"
	}
	q.Where(permissionPredicate(subject, action))
	q.Where(func(sel *entsql.Selector) {
		text, args := visibleSQL(sel.C(resource.FieldID), subject, in.Surface)
		sel.Where(entsql.ExprP(text, args...))
	})
	if in.Tag != "" {
		q.Where(func(sel *entsql.Selector) {
			selected, args := surfaceSQL(sel.C(resource.FieldID), subject, in.Surface)
			query := "(" + sel.C(resource.FieldKind) + "<>'item' AND " + sel.C(resource.FieldID) + " IN (SELECT resource_id FROM resource_tags WHERE tag=?) OR " + sel.C(resource.FieldKind) + "='item' AND EXISTS (SELECT 1 FROM item_surfaces p JOIN item_surface_tags t ON t.surface_id=p.id WHERE p.item_id=" + sel.C(resource.FieldID) + " AND p.surface=" + selected + " AND t.tag=?))"
			values := []any{in.Tag}
			values = append(values, args...)
			values = append(values, in.Tag)
			sel.Where(entsql.ExprP(query, values...))
		})
	}
	if strings.TrimSpace(in.Search) != "" {
		phrase := ftsPhrase(in.Search)
		q.Where(func(sel *entsql.Selector) {
			selected, args := surfaceSQL(sel.C(resource.FieldID), subject, in.Surface)
			query := "(" + sel.C(resource.FieldKind) + "<>'item' AND " + sel.C(resource.FieldID) + " IN (SELECT id FROM resource_search WHERE resource_search MATCH ?) OR " + sel.C(resource.FieldKind) + "='item' AND EXISTS (SELECT 1 FROM item_surfaces p WHERE p.item_id=" + sel.C(resource.FieldID) + " AND p.surface=" + selected + " AND p.id IN (SELECT id FROM item_surface_search WHERE item_surface_search MATCH ?)))"
			values := []any{phrase}
			values = append(values, args...)
			values = append(values, phrase)
			sel.Where(entsql.ExprP(query, values...))
		})
	}
	if in.FilterField != "" {
		predicate, err := s.fieldFilter(ctx, subject, in)
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		q.Where(predicate)
	}
	rows, err := q.Order(ent.Asc(resource.FieldID)).Limit(pageSize(in.Limit) + 1).All(ctx)
	if err != nil {
		return Page[*ent.Resource]{}, err
	}
	out := entityPage(rows, in.Limit, func(v *ent.Resource) string { return v.ID })
	if err = s.overlayPage(ctx, subject, in.Surface, out.Data); err != nil {
		return out, err
	}
	return out, nil
}

// ftsPhrase treats input as a literal phrase, never as executable FTS query syntax.
func ftsPhrase(search string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(search), `"`, `""`) + `"`
}

func (s *Service) overlayPage(ctx context.Context, subject, surface string, rows []*ent.Resource) error {
	ids := []string{}
	items := []*ent.Resource{}
	for _, r := range rows {
		if r.Kind == resource.KindItem {
			ids = append(ids, r.ID)
			items = append(items, r)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", items)
	if err != nil {
		return err
	}
	projections, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDIn(ids...)).All(ctx)
	if err != nil {
		return err
	}
	lookup := map[string]*ent.ItemSurface{}
	revisionIDs := []string{}
	for _, p := range projections {
		lookup[p.ItemID+":"+string(p.Surface)] = p
		revisionIDs = append(revisionIDs, p.RevisionID)
	}
	revisions, err := s.Client.ItemRevision.Query().Where(itemrevision.IDIn(revisionIDs...)).Select(itemrevision.FieldID, itemrevision.FieldCreatedAt).All(ctx)
	if err != nil {
		return err
	}
	revisionLookup := map[string]*ent.ItemRevision{}
	for _, rev := range revisions {
		revisionLookup[rev.ID] = rev
	}
	for _, r := range items {
		selected := "published"
		if surface == "head" || (surface == "auto" && draft[r.ID]) {
			selected = "head"
		}
		p := lookup[r.ID+":"+selected]
		if p == nil {
			return ErrNotFound
		}
		values, err := decodeValues(p.Payload)
		if err != nil {
			return err
		}
		r.Name = p.Name
		r.Tags = p.Tags
		r.Values = values
		if rev := revisionLookup[p.RevisionID]; rev != nil {
			r.UpdatedAt = rev.CreatedAt
		}
		if selected == "published" {
			r.HeadRevisionID = nil
			r.NextRevisionNumber = 0
		}
	}
	return nil
}
