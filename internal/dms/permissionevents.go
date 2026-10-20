package dms

import (
	"context"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
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
func (s *Service) emitPermissionsChanged(ctx context.Context, actor string, r *resources.Resource, data map[string]any) {
	collectionID := r.ContainerID
	if r.Kind == refs.KindList || r.Kind == refs.KindLibrary {
		collectionID = &r.ID
	}
	data["resource_kind"] = string(r.Kind)
	s.emit(ctx, EventPermissionsChanged, actor, r.WorkspaceID, collectionID, r.ID, data)
}

// emitScopeMoved raises permissions.changed with action move when moving r
// gave it another permission scope: its inheriting entries now read through
// another scope.
func (s *Service) emitScopeMoved(ctx context.Context, actor string, before, after *resources.Resource) {
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
	f := resources.Filter{Kinds: contentKinds, State: resources.Live, AfterID: after, Limit: limit}
	switch {
	case action == "member.set" || action == "member.remove":
		f.WorkspaceID = e.WorkspaceID
	default:
		root, err := s.DAL.Resources().Resource(ctx, e.ResourceID)
		if dberr.IsNotFound(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		scope := ""
		if root.ScopeID != nil {
			scope = *root.ScopeID
		}
		switch {
		case root.ScopeID != nil && *root.ScopeID == root.ID:
			// An exclusive scope: what inherits from it carries its ID.
			f.ScopeID = &root.ID
		case root.Kind == refs.KindWorkspace:
			f.WorkspaceID, f.ScopeID = root.ID, &scope
		case root.Kind == refs.KindList || root.Kind == refs.KindLibrary:
			f.ContainerID, f.ScopeID = root.ID, &scope
		default:
			// It inherits again: the entries below it that inherit share the
			// scope of its parent with entries elsewhere, so walk its subtree.
			f.Below, f.ScopeID = root.ID, &scope
		}
	}
	rows, err := s.DAL.Resources().Resources(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]PermissionTarget, len(rows))
	for i, r := range rows {
		out[i] = PermissionTarget{ID: r.ID, Kind: string(r.Kind)}
		if r.ContainerID != nil {
			out[i].CollectionID = *r.ContainerID
		}
	}
	return out, nil
}
