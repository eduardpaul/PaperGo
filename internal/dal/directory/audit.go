package directory

import (
	"context"
	"fmt"
	"papergo/internal/dal/dberr"
)

// AuditState contains only public properties; secrets and usage stamps are excluded.
func (r *Repo) AuditState(ctx context.Context, kind EntityKind, id string) (map[string]any, error) {
	var state map[string]any
	var err error
	switch kind {
	case EntityUser:
		u, e := r.User(ctx, id)
		err = e
		if e == nil {
			state = map[string]any{"display_name": u.DisplayName, "email": u.Email, "active": u.Active, "deleted_at": u.DeletedAt}
		}
	case EntityGroup:
		g, e := r.Group(ctx, id)
		err = e
		if e == nil {
			ps, e := r.Members(ctx, id)
			if e != nil {
				return nil, e
			}
			state = map[string]any{"name": g.Name, "description": g.Description, "members": principalKeys(ps)}
		}
	case EntityRole:
		v, e := r.Role(ctx, id)
		err = e
		if e == nil {
			ps, e := r.Assignments(ctx, id)
			if e != nil {
				return nil, e
			}
			state = map[string]any{"name": v.Name, "description": v.Description, "scopes": v.Scopes, "assignments": principalKeys(ps)}
		}
	case EntityOrganization:
		o, e := r.Organization(ctx)
		err = e
		if e == nil && (id == "" || o.ID == id) {
			state = map[string]any{"identifier": o.Identifier, "display_name": o.DisplayName, "description": o.Description}
		}
	case EntityAccessToken:
		ts, e := r.AccessTokens(ctx, AccessTokenFilter{ID: id})
		err = e
		if e == nil && len(ts) > 0 {
			t := ts[0]
			state = map[string]any{"name": t.Name, "scopes": t.Scopes, "expires_at": t.ExpiresAt, "revoked_at": t.RevokedAt, "subject": t.Subject, "integration": t.Integration, "created_by": t.CreatedBy}
		}
	default:
		return nil, fmt.Errorf("unknown directory audit entity %q", kind)
	}
	if dberr.IsNotFound(err) {
		return nil, nil
	}
	return state, err
}
func principalKeys(ps []Principal) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p.Type) + ":" + p.ID
	}
	return out
}
