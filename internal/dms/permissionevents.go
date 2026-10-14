package dms

import (
	"context"

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
