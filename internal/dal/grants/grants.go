// Package grants owns the access grants of resources with exclusive
// permission scopes, shared by every database driver. A workspace's grants
// are its memberships: one per user, and at least one active owner. Drivers
// implement Queries; this package decides what those statements mean.
package grants

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
)

// PrincipalType is who a grant is for. The workspace roles stand for every
// member of a role in the resource's workspace.
type PrincipalType string

const (
	PrincipalUser             PrincipalType = "user"
	PrincipalGroup            PrincipalType = "group"
	PrincipalWorkspaceVisitor PrincipalType = "workspace_visitor"
	PrincipalWorkspaceMember  PrincipalType = "workspace_member"
	PrincipalWorkspaceOwner   PrincipalType = "workspace_owner"
)

func (p PrincipalType) Valid() bool {
	switch p {
	case PrincipalUser, PrincipalGroup, PrincipalWorkspaceVisitor, PrincipalWorkspaceMember, PrincipalWorkspaceOwner:
		return true
	}
	return false
}

// Action is what a grant allows.
type Action string

const (
	ActionRead      Action = "read"
	ActionReadDraft Action = "read_draft"
	ActionWrite     Action = "write"
	ActionPublish   Action = "publish"
	ActionManage    Action = "manage"
)

func (a Action) Valid() bool {
	switch a {
	case ActionRead, ActionReadDraft, ActionWrite, ActionPublish, ActionManage:
		return true
	}
	return false
}

// membership reports whether a workspace membership may have the action.
func (a Action) membership() bool {
	return a == ActionRead || a == ActionWrite || a == ActionManage
}

// Grant allows a principal an action on a resource. Grants only add access.
type Grant struct {
	ID, ResourceID, PrincipalID string
	PrincipalType               PrincipalType
	Action                      Action
	CreatedAt                   time.Time
}

// Target is the permission state of a resource that receives grants. An
// empty PersonalOwnerID means it is not a Home.
type Target struct {
	Kind                         refs.ResourceKind
	WorkspaceID, PersonalOwnerID string
	Inherit, Deleted             bool
}

// Filter selects grants. They are ordered by principal type, principal and
// action, or by principal alone with ByPrincipal.
type Filter struct {
	ResourceID, PrincipalID, ExcludePrincipalID string
	PrincipalType                               PrincipalType
	Action                                      Action
	ByPrincipal                                 bool
}

// Queries are the statements a driver runs for grants. Each method executes
// on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	// LockDirectory serializes changes to who can access what.
	LockDirectory(ctx context.Context) error
	GrantTarget(ctx context.Context, resourceID string) (Target, error)
	// UserActive reports whether a user is enrolled, active and not deleted.
	UserActive(ctx context.Context, id string) (bool, error)
	FindGrant(ctx context.Context, id string) (Grant, error)
	Grants(ctx context.Context, f Filter) ([]Grant, error)
	InsertGrant(ctx context.Context, g Grant) error
	SetGrantAction(ctx context.Context, id string, action Action) error
	DeleteGrant(ctx context.Context, id string) error
	// DeleteGrants deletes the grants of a resource or principal f selects.
	DeleteGrants(ctx context.Context, f Filter) error
	// OtherWorkspaceOwners counts the active users other than one that own a
	// workspace.
	OtherWorkspaceOwners(ctx context.Context, workspaceID, excludedUserID string) (int64, error)
}

// Repo runs grant reads on its connection and changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds grant rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Grant, error) {
	return r.read.Grants(ctx, f)
}

func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	rows, err := r.read.Grants(ctx, f)
	return len(rows) > 0, err
}

// lockTarget serializes grant changes of a resource and returns its state.
func lockTarget(ctx context.Context, q Queries, resourceID string) (Target, error) {
	if err := q.LockDirectory(ctx); err != nil {
		return Target{}, err
	}
	t, err := q.GrantTarget(ctx, resourceID)
	if err != nil {
		return Target{}, err
	}
	if err := q.LockWorkspace(ctx, t.WorkspaceID); err != nil {
		return Target{}, err
	}
	return q.GrantTarget(ctx, resourceID)
}

// Create grants a principal an action on a resource with an exclusive scope.
// On a workspace it adds the single membership of an existing user, and a
// Home's only membership is its owner's.
func (r *Repo) Create(ctx context.Context, in Grant) (Grant, error) {
	g := Grant{ID: in.ID, ResourceID: in.ResourceID, PrincipalType: in.PrincipalType, PrincipalID: in.PrincipalID, Action: in.Action, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	if !g.Action.Valid() {
		return Grant{}, fmt.Errorf("%w: unsupported grant action %q", dberr.ErrInvalidPermissionScope, g.Action)
	}
	err := r.write(ctx, func(q Queries) error {
		t, err := lockTarget(ctx, q, g.ResourceID)
		if err != nil {
			return err
		}
		if t.Inherit {
			return fmt.Errorf("%w: grants require an exclusive permission scope", dberr.ErrInvalidPermissionScope)
		}
		if err := principal(ctx, q, t, g.PrincipalType, g.PrincipalID, g.Action); err != nil {
			return err
		}
		if t.Kind == refs.KindWorkspace {
			if t.PersonalOwnerID != "" && (t.PersonalOwnerID != g.PrincipalID || g.PrincipalType != PrincipalUser || g.Action != ActionManage) {
				return fmt.Errorf("%w: Home membership is fixed", dberr.ErrInvalidPermissionScope)
			}
			if g.PrincipalType != PrincipalUser || !g.Action.membership() || strings.TrimSpace(g.PrincipalID) == "" || g.PrincipalID != strings.TrimSpace(g.PrincipalID) {
				return fmt.Errorf("%w: invalid workspace membership", dberr.ErrInvalidPermissionScope)
			}
			member, err := q.Grants(ctx, Filter{ResourceID: g.ResourceID, PrincipalID: g.PrincipalID})
			if err != nil {
				return err
			}
			if len(member) > 0 {
				return fmt.Errorf("%w: subject already has a workspace membership grant", dberr.ErrUniqueViolation)
			}
		}
		return q.InsertGrant(ctx, g)
	})
	if err != nil {
		return Grant{}, err
	}
	return g, nil
}

// ChangeAction changes what a grant allows. A workspace keeps an active owner
// and a Home keeps its membership.
func (r *Repo) ChangeAction(ctx context.Context, id string, action Action) (Grant, error) {
	var g Grant
	err := r.write(ctx, func(q Queries) error {
		var err error
		if g, err = q.FindGrant(ctx, id); err != nil {
			return err
		}
		t, err := lockTarget(ctx, q, g.ResourceID)
		if err != nil {
			return err
		}
		if t.PersonalOwnerID != "" {
			return fmt.Errorf("%w: Home membership is fixed", dberr.ErrImmutable)
		}
		if err := principal(ctx, q, t, g.PrincipalType, g.PrincipalID, action); err != nil {
			return err
		}
		if t.Kind == refs.KindWorkspace {
			if !action.membership() {
				return fmt.Errorf("%w: invalid workspace membership action", dberr.ErrInvalidPermissionScope)
			}
			if g.Action == ActionManage && action != ActionManage && !t.Deleted {
				if err := otherOwner(ctx, q, g.ResourceID, g.PrincipalID); err != nil {
					return err
				}
			}
		}
		if err := q.SetGrantAction(ctx, id, action); err != nil {
			return err
		}
		g.Action = action
		return nil
	})
	if err != nil {
		return Grant{}, err
	}
	return g, nil
}

// Revoke deletes a grant. A live workspace keeps an active owner and a live
// Home keeps its membership.
func (r *Repo) Revoke(ctx context.Context, id string) error {
	return r.write(ctx, func(q Queries) error {
		g, err := q.FindGrant(ctx, id)
		if err != nil {
			return err
		}
		t, err := lockTarget(ctx, q, g.ResourceID)
		if err != nil {
			return err
		}
		if t.PersonalOwnerID != "" && !t.Deleted {
			return fmt.Errorf("%w: Home membership is fixed", dberr.ErrImmutable)
		}
		if t.Kind == refs.KindWorkspace && g.Action == ActionManage && !t.Deleted {
			if err := otherOwner(ctx, q, g.ResourceID, g.PrincipalID); err != nil {
				return err
			}
		}
		return q.DeleteGrant(ctx, id)
	})
}

// Clear deletes every grant of a resource that is not a workspace, as when
// its permission scope is replaced or it moves.
func (r *Repo) Clear(ctx context.Context, resourceID string) error {
	return r.write(ctx, func(q Queries) error {
		t, err := q.GrantTarget(ctx, resourceID)
		if err != nil {
			return err
		}
		if t.Kind == refs.KindWorkspace {
			return fmt.Errorf("%w: workspace memberships change one at a time", dberr.ErrInvalidPermissionScope)
		}
		return q.DeleteGrants(ctx, Filter{ResourceID: resourceID})
	})
}

// principal checks that a grant's principal exists and fits its resource.
// Workspace owners must be active users; workspace roles name the resource's
// own workspace.
func principal(ctx context.Context, q Queries, t Target, kind PrincipalType, id string, action Action) error {
	if t.Kind == refs.KindWorkspace && kind == PrincipalUser && action == ActionManage {
		active, err := q.UserActive(ctx, id)
		if err != nil {
			return err
		}
		if !active {
			return fmt.Errorf("%w: workspace owners must be active users", dberr.ErrInvalidPermissionScope)
		}
	}
	switch kind {
	case PrincipalUser, PrincipalGroup:
		ok, err := q.PrincipalExists(ctx, string(kind), id)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: unknown %s principal %q", dberr.ErrInvalidReference, kind, id)
		}
		return nil
	case PrincipalWorkspaceVisitor, PrincipalWorkspaceMember, PrincipalWorkspaceOwner:
		if t.WorkspaceID != id {
			return fmt.Errorf("%w: workspace role principal_id must match resource workspace_id", dberr.ErrInvalidPermissionScope)
		}
		if kind == PrincipalWorkspaceOwner && action != ActionManage {
			return fmt.Errorf("%w: workspace_owner principal requires 'manage' action", dberr.ErrInvalidPermissionScope)
		}
		return nil
	}
	return fmt.Errorf("%w: invalid grant principal_type %q", dberr.ErrInvalidPermissionScope, kind)
}

// otherOwner checks that a workspace keeps an active owner besides a user.
func otherOwner(ctx context.Context, q Queries, workspaceID, userID string) error {
	n, err := q.OtherWorkspaceOwners(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return dberr.ErrLastWorkspaceOwner
	}
	return nil
}
