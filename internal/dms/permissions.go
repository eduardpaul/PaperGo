package dms

import (
	"context"
	entsql "entgo.io/ent/dialect/sql"
	"errors"
	"papergo/ent"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"papergo/internal/identity"
	"strings"
	"time"
)

const (
	MaxFolderDepth   = 64
	MaxResourceDepth = MaxFolderDepth + 3
	MaxTaxonomyDepth = 32
)

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
func permissionSQL(ctx context.Context, idColumn, subject, action string) (string, []any) {
	principal, args := aclPrincipalSQL("g", subject)
	marks, actions := actionParameters(action)
	args = append(args, actions...)
	admin, adminArgs := identity.ScopeSQL(ctx, subject, "workspace.manage")
	module, moduleArgs := modulePermissionSQL(ctx, "acl.kind", subject, action)
	query := `EXISTS (SELECT 1 FROM resources acl JOIN resources workspace ON workspace.id=acl.workspace_id AND workspace.deleted_at IS NULL
 WHERE acl.id=` + idColumn + ` AND acl.deleted_at IS NULL AND (EXISTS(SELECT 1 FROM grants g WHERE g.resource_id=acl.scope_id AND ` + principal + ` AND g.effect='allow' AND g.action IN (` + marks + `))
 OR EXISTS(SELECT 1 FROM grants owner WHERE owner.resource_id=acl.workspace_id AND owner.principal_type='user' AND owner.principal_id=? AND owner.action='manage' AND owner.effect='allow')
 OR ` + admin + `) AND ` + module + `)`
	args = append(args, subject)
	args = append(args, adminArgs...)
	args = append(args, moduleArgs...)
	return query, args
}

func actionParameters(action string) (string, []any) {
	actions := impliedActions(action)
	marks := make([]string, len(actions))
	args := make([]any, len(actions))
	for i, a := range actions {
		marks[i] = "?"
		args[i] = a
	}
	return strings.Join(marks, ","), args
}
func aclPrincipalSQL(alias, subject string) (string, []any) {
	principal, args := identity.PrincipalSQL(alias, subject)
	query := `(` + principal + ` OR (` + alias + `.principal_type IN ('workspace_visitor','workspace_member','workspace_owner') AND EXISTS(SELECT 1 FROM grants membership
 WHERE membership.resource_id=` + alias + `.principal_id AND membership.principal_type='user' AND membership.principal_id=? AND membership.effect='allow'
 AND membership.action=CASE ` + alias + `.principal_type WHEN 'workspace_visitor' THEN 'read' WHEN 'workspace_member' THEN 'write' ELSE 'manage' END)))`
	return query, append(args, subject)
}
func modulePermissionSQL(ctx context.Context, kindColumn, subject, action string) (string, []any) {
	scope := "list.read"
	if action == "write" || action == "publish" {
		scope = "list.write"
	}
	return identity.ScopeExpressionSQL(ctx, subject, `CASE WHEN `+kindColumn+`='workspace' THEN 'workspace.read' WHEN `+kindColumn+` IN ('item','folder') THEN '`+scope+`' ELSE 'list.read' END`)
}

// scopeGrantSQL tests a grant on an ACL scope ID column directly.
func scopeGrantSQL(ctx context.Context, scopeColumn, subject, action string) (string, []any) {
	return permissionSQL(ctx, scopeColumn, subject, action)
}

// permissionPredicate filters resources rows by their own scope_id, which the
// browse indexes cover, so unreadable rows are skipped without a table lookup.
func permissionPredicate(ctx context.Context, subject, action string) func(*entsql.Selector) {
	return func(sel *entsql.Selector) {
		principal, args := aclPrincipalSQL("g", subject)
		marks, actions := actionParameters(action)
		args = append(args, actions...)
		args = append(args, subject)
		admin, adminArgs := identity.ScopeSQL(ctx, subject, "workspace.manage")
		args = append(args, adminArgs...)
		module, moduleArgs := modulePermissionSQL(ctx, sel.C(resource.FieldKind), subject, action)
		args = append(args, moduleArgs...)
		query := `((` + sel.C(resource.FieldScopeID) + ` IN (SELECT g.resource_id FROM grants g WHERE ` + principal + ` AND g.effect='allow' AND g.action IN (` + marks + `)) OR ` + sel.C(resource.FieldWorkspaceID) + ` IN (SELECT g.resource_id FROM grants g JOIN resources w ON w.id=g.resource_id WHERE g.principal_type='user' AND g.principal_id=? AND g.effect='allow' AND g.action='manage' AND w.kind='workspace' AND w.deleted_at IS NULL) OR ` + admin + `) AND ` + module + `)`
		sel.Where(entsql.ExprP(query, args...))
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
	allowed, err := s.Client.Resource.Query().Where(resource.IDIn(ids...), permissionPredicate(ctx, subject, action)).IDs(ctx)
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
	if s.homeProvisioning {
		w, err := s.Client.Resource.Get(ctx, r.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if w.PersonalOwnerID != nil && *w.PersonalOwnerID == subject {
			return r, nil
		}
		return nil, ErrForbidden
	}
	allowed, err := s.allowedMany(ctx, subject, action, []*ent.Resource{r})
	if err != nil {
		return nil, err
	}
	if !allowed[id] {
		if r.Kind == resource.KindWorkspace {
			return nil, ErrNotFound
		}
		return nil, ErrForbidden
	}
	return r, nil
}
func surfaceSQL(ctx context.Context, idColumn, subject, surface string) (string, []any) {
	if surface == "head" || surface == "published" {
		return "'" + surface + "'", nil
	}
	query, args := permissionSQL(ctx, idColumn, subject, "read_draft")
	return "CASE WHEN " + query + " THEN 'head' ELSE 'published' END", args
}

// When an item has both surfaces, either selection is visible, so the cheap
// index probes short-circuit the recursive read_draft walk in surfaceSQL.
func visibleSQL(ctx context.Context, idColumn, subject, surface string) (string, []any) {
	selected, args := surfaceSQL(ctx, idColumn, subject, surface)
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
	if r.Kind == resource.KindFolder {
		if err = s.overlayHead(ctx, r); err != nil {
			return nil, err
		}
		return r, nil
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
	projection, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(id), itemsurface.RevisionIDEQ(*selected)).First(ctx)
	if err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339Nano, projection.ModifiedAt); err != nil {
		return nil, err
	}
	r.UpdatedBy = projection.ModifiedBy
	if !draft[id] || surface == "published" {
		r.HeadRevisionID = nil
		r.NextRevisionNumber = 0
	}
	return r, nil
}

// AccessActions lists the actions in the order the access field reports them.
var AccessActions = []string{"read", "read_draft", "write", "publish", "manage"}

// Access returns the actions the subject may take on r, so clients can offer
// only those. It applies the same ACL and module-scope rules as the writes:
// adding or changing content (write, publish) below a workspace also needs the
// list.write scope.
func (s *Service) Access(ctx context.Context, subject string, r *ent.Resource) ([]string, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]string, error) { return t.Access(ctx, subject, r) })
	}
	contentWrite := true
	if r.Kind != resource.KindWorkspace {
		if err := s.RequireScope(ctx, subject, "list.write"); errors.Is(err, ErrForbidden) {
			contentWrite = false
		} else if err != nil {
			return nil, err
		}
	}
	out := []string{}
	for _, action := range AccessActions {
		if (action == "write" || action == "publish") && !contentWrite {
			continue
		}
		allowed, err := s.allowedMany(ctx, subject, action, []*ent.Resource{r})
		if err != nil {
			return nil, err
		}
		if allowed[r.ID] {
			out = append(out, action)
		}
	}
	return out, nil
}
