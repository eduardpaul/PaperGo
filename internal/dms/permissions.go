package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/resource"
	"strings"
)

const MaxDepth = 32

func impliedActions(action string) []string {
	switch action {
	case "read":
		return []string{"read", "read_draft", "write", "publish", "manage"}
	case "read_draft":
		return []string{"read_draft", "write", "publish", "manage"}
	case "write":
		return []string{"write", "manage"}
	case "publish":
		return []string{"publish", "manage"}
	default:
		return []string{action}
	}
}

// This SQL is isolated so a future database adapter can replace it. Additive
// grants apply at the nearest exclusive scope; inherited resources have no ACL.
func permissionSQL(idColumn, subject, action string) (string, []any) {
	actions := impliedActions(action)
	args := []any{subject}
	marks := make([]string, len(actions))
	for i, a := range actions {
		marks[i] = "?"
		args = append(args, a)
	}
	// scope_id, maintained by triggers, is the nearest exclusive ancestor-or-self.
	query := `EXISTS (SELECT 1 FROM resources acl JOIN grants g ON g.resource_id=acl.scope_id
 WHERE acl.id=` + idColumn + ` AND g.subject=? AND g.effect='allow' AND g.action IN (` + strings.Join(marks, ",") + `))`
	return query, args
}

// scopeGrantSQL tests a grant on an ACL scope ID column directly.
func scopeGrantSQL(scopeColumn, subject, action string) (string, []any) {
	actions := impliedActions(action)
	args := []any{subject}
	marks := make([]string, len(actions))
	for i, a := range actions {
		marks[i] = "?"
		args = append(args, a)
	}
	return `EXISTS (SELECT 1 FROM grants g WHERE g.resource_id=` + scopeColumn + ` AND g.subject=? AND g.effect='allow' AND g.action IN (` + strings.Join(marks, ",") + `))`, args
}

// permissionPredicate filters resources rows by their own scope_id, which the
// browse indexes cover, so unreadable rows are skipped without a table lookup.
func permissionPredicate(subject, action string) func(*entsql.Selector) {
	return func(sel *entsql.Selector) {
		actions := impliedActions(action)
		args := []any{subject}
		marks := make([]string, len(actions))
		for i, a := range actions {
			marks[i] = "?"
			args = append(args, a)
		}
		sel.Where(entsql.ExprP(sel.C(resource.FieldScopeID)+` IN (SELECT resource_id FROM grants WHERE subject=? AND effect='allow' AND action IN (`+strings.Join(marks, ",")+`))`, args...))
	}
}
func (s *Service) allowedMany(ctx context.Context, subject, action string, roots []*ent.Resource) (map[string]bool, error) {
	out := map[string]bool{}
	if len(roots) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(roots))
	for _, r := range roots {
		ids = append(ids, r.ID)
	}
	allowed, err := s.Client.Resource.Query().Where(resource.IDIn(ids...), permissionPredicate(subject, action)).IDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range allowed {
		out[id] = true
	}
	return out, nil
}
func (s *Service) authorize(ctx context.Context, subject, id, action string) (*ent.Resource, error) {
	r, err := s.Client.Resource.Get(ctx, id)
	if ent.IsNotFound(err) || err == nil && r.DeletedAt != nil {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	allowed, err := s.allowedMany(ctx, subject, action, []*ent.Resource{r})
	if err != nil {
		return nil, err
	}
	if !allowed[id] {
		return nil, ErrForbidden
	}
	return r, nil
}
func surfaceSQL(idColumn, subject, surface string) (string, []any) {
	if surface == "head" || surface == "published" {
		return "'" + surface + "'", nil
	}
	query, args := permissionSQL(idColumn, subject, "read_draft")
	return "CASE WHEN " + query + " THEN 'head' ELSE 'published' END", args
}

// When an item has both surfaces, either selection is visible, so the cheap
// index probes short-circuit the recursive read_draft walk in surfaceSQL.
func visibleSQL(idColumn, subject, surface string) (string, []any) {
	selected, args := surfaceSQL(idColumn, subject, surface)
	both := ""
	if surface != "head" && surface != "published" {
		both = ` OR EXISTS (SELECT 1 FROM item_surfaces vh WHERE vh.item_id=vr.id AND vh.surface='head') AND EXISTS (SELECT 1 FROM item_surfaces vp WHERE vp.item_id=vr.id AND vp.surface='published')`
	}
	query := `EXISTS (SELECT 1 FROM resources vr WHERE vr.id=` + idColumn + ` AND (vr.kind<>'item'` + both + ` OR EXISTS (SELECT 1 FROM item_surfaces vs WHERE vs.item_id=vr.id AND vs.surface=` + selected + `)))`
	return query, args
}
func (s *Service) GetSurface(ctx context.Context, subject, id, surface string) (*ent.Resource, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.Resource, error) { return t.GetSurface(ctx, subject, id, surface) })
	}
	if surface == "" {
		surface = "auto"
	}
	if surface != "auto" && surface != "head" && surface != "published" {
		return nil, invalid("surface must be auto, head or published")
	}
	r, err := s.authorize(ctx, subject, id, "read")
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindItem {
		return r, nil
	}
	draft, err := s.allowedMany(ctx, subject, "read_draft", []*ent.Resource{r})
	if err != nil {
		return nil, err
	}
	if surface == "head" && !draft[id] {
		return nil, ErrForbidden
	}
	selected := r.PublishedRevisionID
	if surface == "head" || (surface == "auto" && draft[id]) {
		selected = r.HeadRevisionID
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	revision, err := s.Client.ItemRevision.Get(ctx, *selected)
	if err != nil {
		return nil, err
	}
	if err = overlayRevision(r, revision); err != nil {
		return nil, err
	}
	if !draft[id] || surface == "published" {
		r.HeadRevisionID = nil
		r.NextRevisionNumber = 0
	}
	return r, nil
}
