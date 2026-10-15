package dms

import (
	"context"
	"errors"

	"papergo/ent"
	"papergo/ent/resource"
)

// samePermissions compares local ACL state; order and an omitted allow effect
// do not change a grant's meaning.
func samePermissions(a, b Permissions) bool {
	if a.Inherit != b.Inherit || len(a.Grants) != len(b.Grants) {
		return false
	}
	grants := make(map[Permission]bool, len(a.Grants))
	for _, g := range a.Grants {
		g.Effect = "allow"
		grants[g] = true
	}
	for _, g := range b.Grants {
		g.Effect = "allow"
		if !grants[g] {
			return false
		}
	}
	return true
}

// One event names the edited scope, rather than every inheriting descendant.
func (s *Service) emitPermissionsChanged(ctx context.Context, actor string, r *ent.Resource, data map[string]any) {
	collectionID := r.ContainerID
	if r.Kind == resource.KindList || r.Kind == resource.KindLibrary {
		collectionID = &r.ID
	}
	data["resource_kind"] = string(r.Kind)
	s.emit(ctx, EventPermissionsChanged, actor, r.WorkspaceID, collectionID, r.ID, data)
}

// emitScopeMoved raises permissions.changed with action move when moving r
// gave it another permission scope: its inheriting entries now read through
// another scope.
func (s *Service) emitScopeMoved(ctx context.Context, actor string, before, after *ent.Resource) {
	if sameScope(before.ScopeID, after.ScopeID) {
		return
	}
	s.emitPermissionsChanged(ctx, actor, after, map[string]any{
		"action": "move", "previous_scope_id": scopeText(before.ScopeID), "current_scope_id": scopeText(after.ScopeID),
	})
}

func sameScope(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func scopeText(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// PermissionTarget is a live item or folder whose effective permissions a
// permissions.changed root event changed.
type PermissionTarget struct {
	ID, Kind, CollectionID string
}

// PermissionTargets lists, after the resource ID after and in ID order, at
// most limit items and folders whose effective permissions the root
// permissions.changed event e changed:
//   - an ACL replace or a move of a resource: the resource and the entries
//     below it that inherit from it, stopping at entries with their own
//     permissions. They share its permission scope after the change;
//   - a workspace membership change: every item and folder of the workspace,
//     because grants to workspace roles can appear in any scope.
//
// The rows are read as they are now, so later changes are reflected.
func (s *Service) PermissionTargets(ctx context.Context, e Event, after string, limit int) ([]PermissionTarget, error) {
	action, _ := e.Data["action"].(string)
	var where string
	var args []any
	switch {
	case action == "member.set" || action == "member.remove":
		where, args = "r.workspace_id=?", []any{e.WorkspaceID}
	default:
		root, err := s.Client.Resource.Get(ctx, e.ResourceID)
		if ent.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		switch {
		case root.ScopeID != nil && *root.ScopeID == root.ID:
			// An exclusive scope: what inherits from it carries its ID.
			where, args = "r.scope_id=?", []any{root.ID}
		case root.Kind == resource.KindWorkspace:
			where, args = "r.workspace_id=? AND r.scope_id IS ?", []any{root.ID, root.ScopeID}
		case root.Kind == resource.KindList || root.Kind == resource.KindLibrary:
			where, args = "r.container_id=? AND r.scope_id IS ?", []any{root.ID, root.ScopeID}
		default:
			// It inherits again: the entries below it that inherit share the
			// scope of its parent with entries elsewhere, so walk its subtree.
			where = "r.id IN (WITH RECURSIVE subtree(id) AS (SELECT ? UNION ALL SELECT c.id FROM resources c JOIN subtree ON c.parent_id=subtree.id) SELECT id FROM subtree) AND r.scope_id IS ?"
			args = []any{root.ID, root.ScopeID}
		}
	}
	rows, err := s.Client.QueryContext(ctx, `SELECT r.id, r.kind, coalesce(r.container_id,'') FROM resources r WHERE `+where+
		` AND r.kind IN ('item','folder') AND r.deleted_at IS NULL AND r.id>? ORDER BY r.id LIMIT ?`, append(args, after, limit)...)
	if err != nil {
		return nil, err
	}
	var out []PermissionTarget
	for rows.Next() {
		var t PermissionTarget
		if err = rows.Scan(&t.ID, &t.Kind, &t.CollectionID); err != nil {
			break
		}
		out = append(out, t)
	}
	return out, errors.Join(err, rows.Err(), rows.Close())
}
