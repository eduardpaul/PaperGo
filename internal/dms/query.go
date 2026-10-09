package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/predicate"
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
	q := s.Client.Resource.Query().Where(resource.DeletedAtIsNil())
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
	var fieldPredicate func(*entsql.Selector)
	candidates := []candidateSet{}
	if in.FilterField != "" {
		predicate, candidate, err := s.fieldFilter(ctx, subject, in)
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		fieldPredicate = predicate
		candidates = append(candidates, candidate)
	}
	if in.Tag != "" {
		candidates = append(candidates, candidateSet{
			sql:          "SELECT resource_id FROM resource_tags WHERE tag=? UNION ALL SELECT p.item_id FROM item_surface_tags t JOIN item_surfaces p ON p.id=t.surface_id WHERE t.tag=?",
			args:         []any{in.Tag, in.Tag},
			estimate:     "SELECT 1 FROM resource_tags WHERE tag=? UNION ALL SELECT 1 FROM item_surface_tags WHERE tag=?",
			estimateArgs: []any{in.Tag, in.Tag},
		})
	}
	if strings.TrimSpace(in.Search) != "" {
		phrase := ftsPhrase(in.Search)
		candidates = append(candidates, candidateSet{
			sql:          "SELECT rr.id FROM resources rr WHERE rr.rowid IN (SELECT rowid FROM resource_search WHERE resource_search MATCH ?) AND rr.kind<>'item' UNION ALL SELECT s.item_id FROM item_surfaces s WHERE s.rowid IN (SELECT rowid FROM item_surface_search WHERE item_surface_search MATCH ?)",
			args:         []any{phrase, phrase},
			estimate:     "SELECT 1 FROM resource_search WHERE resource_search MATCH ? UNION ALL SELECT 1 FROM item_surface_search WHERE item_surface_search MATCH ?",
			estimateArgs: []any{phrase, phrase},
		})
	}
	for _, c := range candidates {
		small, err := s.selective(ctx, c)
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		if small {
			q.Where(func(sel *entsql.Selector) {
				sel.Where(entsql.ExprP(sel.C(resource.FieldID)+" IN ("+c.sql+")", c.args...))
			})
		}
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
			// FTS rowids are content-table rowids; the UNINDEXED id column would cost a lookup per match.
			query := "(" + sel.C(resource.FieldKind) + "<>'item' AND " + sel.C("rowid") + " IN (SELECT rowid FROM resource_search WHERE resource_search MATCH ?) OR " + sel.C(resource.FieldKind) + "='item' AND EXISTS (SELECT 1 FROM item_surfaces p WHERE p.item_id=" + sel.C(resource.FieldID) + " AND p.surface=" + selected + " AND p.rowid IN (SELECT rowid FROM item_surface_search WHERE item_surface_search MATCH ?)))"
			values := []any{phrase}
			values = append(values, args...)
			values = append(values, phrase)
			sel.Where(entsql.ExprP(query, values...))
		})
	}
	if fieldPredicate != nil {
		q.Where(fieldPredicate)
	}
	rows, err := q.Order(ent.Asc(resource.FieldID)).Limit(pageSize(in.Limit) + 1).All(ctx)
	if err != nil {
		return Page[*ent.Resource]{}, err
	}
	out := entityPage(rows, in.Limit, func(v *ent.Resource) string { return v.ID })
	if _, err = s.overlayPage(ctx, subject, in.Surface, out.Data, nil); err != nil {
		return out, err
	}
	return out, nil
}

// candidateSet is an index-only superset of the resource IDs a filter can match.
// A small set drives the query, so per-row ACL walks run only on candidates; a
// large one is left to the ID-ordered scan, which fills a page after few rows.
type candidateSet struct {
	sql, estimate      string
	args, estimateArgs []any
}

var candidateDriveLimit = 1000

// selective counts at most candidateDriveLimit+1 index entries, so it stays cheap for broad filters.
func (s *Service) selective(ctx context.Context, c candidateSet) (bool, error) {
	rows, err := s.Client.QueryContext(ctx, "SELECT count(*) FROM ("+c.estimate+" LIMIT ?)", append(append([]any{}, c.estimateArgs...), candidateDriveLimit+1)...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	n := 0
	if rows.Next() {
		if err = rows.Scan(&n); err != nil {
			return false, err
		}
	}
	return n <= candidateDriveLimit, rows.Err()
}

// ftsPhrase treats input as a literal phrase, never as executable FTS query syntax.
func ftsPhrase(search string) string {
	return `"` + strings.ReplaceAll(strings.TrimSpace(search), `"`, `""`) + `"`
}

// overlayPage applies each item's selected surface and returns the selected revision
// of every item, keyed by item ID, with its creation time and blob. Only the
// selected surface row is read. A non-nil fields limits Values to those keys,
// extracted in SQL, so list rows never decode payloads they discard.
func (s *Service) overlayPage(ctx context.Context, subject, surface string, rows []*ent.Resource, fields []string) (map[string]*ent.ItemRevision, error) {
	ids := []string{}
	items := []*ent.Resource{}
	for _, r := range rows {
		if r.Kind == resource.KindItem {
			ids = append(ids, r.ID)
			items = append(items, r)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", items)
	if err != nil {
		return nil, err
	}
	selectedSurface := func(r *ent.Resource) itemsurface.Surface {
		if surface == "head" || (surface == "auto" && draft[r.ID]) {
			return itemsurface.SurfaceHead
		}
		return itemsurface.SurfacePublished
	}
	bySurface := map[itemsurface.Surface][]string{}
	for _, r := range items {
		bySurface[selectedSurface(r)] = append(bySurface[selectedSurface(r)], r.ID)
	}
	selectors := []predicate.ItemSurface{}
	for sf, members := range bySurface {
		selectors = append(selectors, itemsurface.And(itemsurface.SurfaceEQ(sf), itemsurface.ItemIDIn(members...)))
	}
	q := s.Client.ItemSurface.Query().Where(itemsurface.Or(selectors...))
	if fields != nil {
		columns := []string{}
		for _, c := range itemsurface.Columns {
			if c != itemsurface.FieldPayload {
				columns = append(columns, c)
			}
		}
		q.Select(columns...)
	}
	projections, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	lookup := map[string]*ent.ItemSurface{}
	revisionIDs := []string{}
	for _, p := range projections {
		lookup[p.ItemID] = p
		revisionIDs = append(revisionIDs, p.RevisionID)
	}
	if fields != nil {
		if err = s.projectPayloads(ctx, projections, fields); err != nil {
			return nil, err
		}
	}
	revisions, err := s.Client.ItemRevision.Query().Where(itemrevision.IDIn(revisionIDs...)).Select(itemrevision.FieldID, itemrevision.FieldCreatedAt, itemrevision.FieldBlobID).All(ctx)
	if err != nil {
		return nil, err
	}
	revisionLookup := map[string]*ent.ItemRevision{}
	for _, rev := range revisions {
		revisionLookup[rev.ID] = rev
	}
	selectedRevisions := make(map[string]*ent.ItemRevision, len(items))
	for _, r := range items {
		selected := selectedSurface(r)
		p := lookup[r.ID]
		if p == nil {
			return nil, ErrNotFound
		}
		values, err := decodeValues(p.Payload)
		if err != nil {
			return nil, err
		}
		r.Name = p.Name
		r.Tags = p.Tags
		r.Values = values
		r.UpdatedBy = p.ModifiedBy
		if rev := revisionLookup[p.RevisionID]; rev != nil {
			r.UpdatedAt = rev.CreatedAt
			selectedRevisions[r.ID] = rev
		}
		if selected == itemsurface.SurfacePublished {
			r.HeadRevisionID = nil
			r.NextRevisionNumber = 0
		}
	}
	return selectedRevisions, nil
}

// projectPayloads replaces each surface payload with an object of just the
// requested keys that it holds, read with SQLite's -> operator.
func (s *Service) projectPayloads(ctx context.Context, surfaces []*ent.ItemSurface, fields []string) error {
	byID := make(map[string]*ent.ItemSurface, len(surfaces))
	for _, p := range surfaces {
		p.Payload = json.RawMessage("{}")
		byID[p.ID] = p
	}
	if len(fields) == 0 || len(surfaces) == 0 {
		return nil
	}
	columns := make([]string, len(fields))
	args := make([]any, 0, len(fields)+len(surfaces))
	for i, key := range fields {
		columns[i] = ",payload->?"
		args = append(args, `$."`+key+`"`)
	}
	marks := make([]string, len(surfaces))
	for i, p := range surfaces {
		marks[i] = "?"
		args = append(args, p.ID)
	}
	rows, err := s.Client.QueryContext(ctx, "SELECT id"+strings.Join(columns, "")+" FROM item_surfaces WHERE id IN ("+strings.Join(marks, ",")+")", args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		values := make([]sql.NullString, len(fields))
		dest := []any{&id}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err = rows.Scan(dest...); err != nil {
			return err
		}
		object := map[string]json.RawMessage{}
		for i, v := range values {
			if v.Valid {
				object[fields[i]] = json.RawMessage(v.String)
			}
		}
		if byID[id].Payload, err = json.Marshal(object); err != nil {
			return err
		}
	}
	return rows.Err()
}
