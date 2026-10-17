package dms

import (
	"context"
	"database/sql"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"maps"
	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/predicate"
	"papergo/ent/resource"
	"slices"
	"strings"
	"time"
)

type Page[T any] struct {
	Data       []T    `json:"value"`
	NextCursor string `json:"-"`
	// SearchCapped means the search text matched more entries than a
	// listing considers; narrow the words or filters for complete results.
	SearchCapped bool `json:"search_capped,omitempty"`
}
type Browse struct {
	ParentID    string
	WorkspaceID string
	Search      string
	Keyword     string
	After       string
	Limit       int
	Surface     string
	FilterField string
	FilterOp    string
	FilterValue string
	// Kind keeps one kind of resource, e.g. the folders of a large library.
	Kind string
}

func pageSize(limit int) int {
	if limit < 1 {
		return 50
	}
	if limit > 500 {
		return 500
	}
	return limit
}

func (s *Service) Browse(ctx context.Context, subject string, in Browse) (Page[*ent.Resource], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*ent.Resource], error) { return t.Browse(ctx, subject, in) })
	}
	scope := "list.read"
	if in.WorkspaceID == "" && in.ParentID == "" {
		scope = "workspace.read"
	}
	if err := s.RequireScope(ctx, subject, scope); err != nil {
		return Page[*ent.Resource]{}, err
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
	if in.Kind != "" && resource.KindValidator(resource.Kind(in.Kind)) != nil {
		return Page[*ent.Resource]{}, invalid("kind must be workspace, list, library, folder or item")
	}
	if in.Keyword != "" {
		var err error
		in.Keyword, err = s.keywordFilter(ctx, in.Keyword)
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
	}
	q := s.Client.Resource.Query().Where(resource.DeletedAtIsNil())
	// Search text matches items and folders in the search store, within the
	// parent's collection or the workspace, and other resources by name.
	var matchCollection, matchWorkspace string
	if in.ParentID != "" {
		parent, err := s.authorize(ctx, subject, in.ParentID, "read")
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		q.Where(resource.ParentIDEQ(in.ParentID))
		switch {
		case parent.Kind == resource.KindList || parent.Kind == resource.KindLibrary:
			matchCollection = parent.ID
		case parent.ContainerID != nil:
			matchCollection = *parent.ContainerID
		default:
			matchWorkspace = parent.WorkspaceID
		}
	} else if in.WorkspaceID != "" {
		matchWorkspace = in.WorkspaceID
		w, err := s.authorize(ctx, subject, in.WorkspaceID, "read")
		if err != nil {
			return Page[*ent.Resource]{}, err
		}
		if w.Kind != resource.KindWorkspace {
			return Page[*ent.Resource]{}, invalid("workspace_id must reference a workspace")
		}
		q.Where(resource.WorkspaceIDEQ(in.WorkspaceID))
	} else {
		if in.Search != "" || in.Keyword != "" {
			return Page[*ent.Resource]{}, invalid("search and keywords require workspace_id or parent_id")
		}
		q.Where(resource.KindEQ(resource.KindWorkspace))
	}
	if in.Kind != "" {
		q.Where(resource.KindEQ(resource.Kind(in.Kind)))
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

	if in.Keyword != "" {
		aliases := "SELECT id FROM terms WHERE id=? OR merged_into_id=?"
		keywordQuery := "SELECT resource_id AS id FROM resource_keywords WHERE keyword IN (" + aliases + ") UNION ALL SELECT p.item_id FROM item_surface_keywords k JOIN item_surfaces p ON p.id=k.surface_id WHERE k.keyword IN (" + aliases + ") UNION ALL SELECT item_id FROM field_values WHERE field_type IN ('keywords','term') AND value_text IN (" + aliases + ")"
		args := []any{in.Keyword, in.Keyword, in.Keyword, in.Keyword, in.Keyword, in.Keyword}
		candidates = append(candidates, candidateSet{sql: keywordQuery, args: args, estimate: keywordQuery, estimateArgs: args})
	}

	var head, published, name string
	capped := false
	if strings.TrimSpace(in.Search) != "" {
		var err error
		if head, published, capped, err = s.searchMatches(ctx, subject, matchCollection, matchWorkspace, in.Search); err != nil {
			return Page[*ent.Resource]{}, err
		}
		name = strings.ToLower(strings.TrimSpace(in.Search))
		candidates = append(candidates, candidateSet{
			sql:          "SELECT value FROM json_each(?) UNION ALL SELECT value FROM json_each(?) UNION ALL SELECT id FROM resources WHERE kind IN ('workspace','list','library') AND instr(lower(name),?)>0",
			args:         []any{head, published, name},
			estimate:     "SELECT 1 FROM json_each(?) UNION ALL SELECT 1 FROM json_each(?) UNION ALL SELECT 1 FROM resources WHERE kind IN ('workspace','list','library') AND instr(lower(name),?)>0",
			estimateArgs: []any{head, published, name},
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
	q.Where(permissionPredicate(ctx, subject, action))
	q.Where(func(sel *entsql.Selector) {
		text, args := visibleSQL(ctx, sel.C(resource.FieldID), subject, in.Surface)
		sel.Where(entsql.ExprP(text, args...))
	})
	if in.Keyword != "" {
		q.Where(func(sel *entsql.Selector) {
			selected, a := surfaceSQL(ctx, sel.C(resource.FieldID), subject, in.Surface)
			query := "(" + sel.C(resource.FieldKind) + " NOT IN ('item','folder') AND EXISTS(SELECT 1 FROM resource_keywords k JOIN terms kt ON kt.id=k.keyword WHERE k.resource_id=" + sel.C(resource.FieldID) + " AND coalesce(kt.merged_into_id,kt.id)=?) OR " + sel.C(resource.FieldKind) + " IN ('item','folder') AND EXISTS (SELECT 1 FROM item_surfaces p WHERE p.item_id=" + sel.C(resource.FieldID) + " AND p.surface=" + selected + " AND EXISTS(SELECT 1 FROM (" + surfaceKeywordsSQL + ") k JOIN terms kt ON kt.id=k.keyword WHERE coalesce(kt.merged_into_id,kt.id)=?)))"
			args := append([]any{in.Keyword}, a...)
			args = append(args, in.Keyword)
			sel.Where(entsql.ExprP(query, args...))
		})
	}
	if strings.TrimSpace(in.Search) != "" {
		q.Where(func(sel *entsql.Selector) {
			selected, args := surfaceSQL(ctx, sel.C(resource.FieldID), subject, in.Surface)
			// An entry matches on the surface the caller reads.
			query := "(" + sel.C(resource.FieldKind) + " NOT IN ('item','folder') AND instr(lower(" + sel.C(resource.FieldName) + "),?)>0 OR " + sel.C(resource.FieldKind) + " IN ('item','folder') AND " +
				sel.C(resource.FieldID) + " IN (SELECT value FROM json_each(CASE " + selected + " WHEN 'head' THEN ? ELSE ? END)))"
			values := []any{name}
			values = append(values, args...)
			values = append(values, head, published)
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
	out.SearchCapped = capped
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

// overlayPage applies each item's selected surface and returns the selected revision
// of every item, keyed by item ID, with its creation time and blob. Only the
// selected surface row is read. A non-nil fields limits Values to those keys,
// extracted in SQL, so list rows never decode payloads they discard.
func (s *Service) overlayPage(ctx context.Context, subject, surface string, rows []*ent.Resource, fields []string) (map[string]*ent.ItemRevision, error) {
	ids := []string{}
	items := []*ent.Resource{}
	for _, r := range rows {
		if contentKind(r.Kind) {
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
	// Items of collections without publishing have one surface, the published one.
	containers := map[string]bool{}
	for _, r := range items {
		if r.ContainerID != nil {
			containers[*r.ContainerID] = true
		}
	}
	publishing, err := s.Client.Resource.Query().Where(resource.IDIn(slices.Collect(maps.Keys(containers))...), resource.PublishingEnabled(true)).IDs(ctx)
	if err != nil {
		return nil, err
	}
	publishes := map[string]bool{}
	for _, id := range publishing {
		publishes[id] = true
	}
	wantsHead := func(r *ent.Resource) bool { return surface == "head" || (surface == "auto" && draft[r.ID]) }
	selectedSurface := func(r *ent.Resource) itemsurface.Surface {
		if r.ContainerID != nil && publishes[*r.ContainerID] && wantsHead(r) {
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
		r.Keywords = p.Keywords
		r.Values = values
		r.UpdatedBy = p.ModifiedBy
		if rev := revisionLookup[p.RevisionID]; rev != nil {
			if r.UpdatedAt, err = time.Parse(time.RFC3339Nano, p.ModifiedAt); err != nil {
				return nil, err
			}
			selectedRevisions[r.ID] = rev
		}
		// Readers of published content do not learn about drafts.
		if selected == itemsurface.SurfacePublished && !wantsHead(r) {
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
