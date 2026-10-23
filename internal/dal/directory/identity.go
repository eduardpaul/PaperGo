package directory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
)

const MaxGroupDepth = 10

var ErrManagedGroup = errors.New("group membership is managed by the identity provider")

type Principal struct {
	Type PrincipalType `json:"principal_type"`
	ID   string        `json:"principal_id"`
}
type Role struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	Builtin     bool     `json:"builtin"`
	Version     int      `json:"version"`
}
type Organization struct {
	ID, Identifier, DisplayName, Description string
	Version                                  int64
}
type Preferences struct {
	Values  map[string]string
	Version int64
}
type MembershipFilter struct {
	GroupID   string
	Principal *Principal
}

type Membership struct {
	ID, GroupID string
	Principal   Principal
}
type AccessToken struct {
	ID, Subject, Name, Prefix, SecretHash, CreatedBy string
	Scopes                                           []string
	// Resources limits the token to these workspaces, lists and libraries;
	// empty is unlimited.
	Resources                        []string
	Integration                      bool
	CreatedAt                        time.Time
	ExpiresAt, LastUsedAt, RevokedAt *time.Time
}
type AccessTokenFilter struct {
	Subject        string
	Personal, Live bool
	ID, SecretHash string
	Now            time.Time
}

// IdentityQueries contains directory persistence only; rules live in Repo.
type IdentityQueries interface {
	DirectoryEffectiveGroups(context.Context, string) ([]string, error)
	DirectoryHasAdministrator(context.Context, string) (bool, error)
	DirectoryEffectiveScopes(context.Context, string, string, time.Time) ([]string, error)
	LockDirectory(context.Context) error
	InsertDirectoryUser(context.Context, User) error
	UpdateDirectoryUser(context.Context, User) error
	SetDirectoryUserActive(context.Context, string, bool) error
	SetDirectoryPassword(context.Context, string, string) error
	DeleteDirectoryUserData(context.Context, string, time.Time) error
	UserIsLastWorkspaceOwner(context.Context, string) (bool, error)
	UserHasHome(context.Context, string) (bool, error)
	DirectoryGroups(context.Context) ([]Group, error)
	InsertDirectoryGroup(context.Context, Group) error
	UpdateDirectoryGroup(context.Context, Group, int) (bool, error)
	DeleteDirectoryGroup(context.Context, string, int) (bool, error)
	DirectoryMemberships(context.Context, MembershipFilter) ([]Membership, error)
	InsertDirectoryMembership(context.Context, Membership) error
	DeleteDirectoryMembership(context.Context, string, Principal) error
	DirectoryRoles(context.Context) ([]Role, error)
	InsertDirectoryRole(context.Context, Role) error
	UpdateDirectoryRole(context.Context, Role, int) (bool, error)
	DeleteDirectoryRole(context.Context, string, int) (bool, error)
	RolePrincipals(context.Context, string) ([]Principal, error)
	InsertDirectoryAssignment(context.Context, string, Principal) error
	DeleteDirectoryAssignment(context.Context, string, Principal) error
	DirectoryOrganization(context.Context) (Organization, error)
	InsertDirectoryOrganization(context.Context) error
	UpdateDirectoryOrganization(context.Context, Organization, int64) (bool, error)
	DirectoryPreferences(context.Context, PreferenceOwner, string) (Preferences, error)
	WriteDirectoryPreferences(context.Context, PreferenceOwner, string, int64, map[string]string) (bool, error)
	DirectoryAccessTokens(context.Context, AccessTokenFilter) ([]AccessToken, error)
	InsertDirectoryAccessToken(context.Context, AccessToken) error
	RevokeDirectoryAccessToken(context.Context, string, string, bool, time.Time) (bool, error)
	TouchDirectoryAccessToken(context.Context, string, time.Time) error
}

// Lock joins a caller-owned transaction's directory lock. SQLite callers must
// reserve their writer before reading; Store transactions do this automatically.
func (r *Repo) Lock(ctx context.Context) error { return r.read.LockDirectory(ctx) }
func (r *Repo) change(ctx context.Context, fn func(Queries) error) error {
	return r.write(ctx, func(q Queries) error {
		if err := q.LockDirectory(ctx); err != nil {
			return err
		}
		return fn(q)
	})
}
func (r *Repo) CreateUser(ctx context.Context, id, name, email string, active, service bool) (User, error) {
	u := User{ID: id, DisplayName: name, Email: email, Active: active, Service: service, CreatedAt: time.Now().UTC()}
	err := r.change(ctx, func(q Queries) error { return q.InsertDirectoryUser(ctx, u) })
	return u, err
}
func (r *Repo) SaveUser(ctx context.Context, u User) error {
	return r.change(ctx, func(q Queries) error {
		old, err := q.DirectoryUser(ctx, u.ID)
		if err != nil {
			return err
		}
		if old.DeletedAt != nil {
			return dberr.ErrNotFound
		}
		return q.UpdateDirectoryUser(ctx, u)
	})
}
func (r *Repo) SetUserActive(ctx context.Context, id string, active bool) error {
	return r.change(ctx, func(q Queries) error { return q.SetDirectoryUserActive(ctx, id, active) })
}
func (r *Repo) LocalPasswordHash(ctx context.Context, id string) (string, error) {
	u, e := r.User(ctx, id)
	return u.PasswordHash, e
}
func (r *Repo) InitializeLocalPassword(ctx context.Context, id, hash string) error {
	if hash == "" {
		return dberr.ErrCheckViolation
	}
	return r.change(ctx, func(q Queries) error {
		if _, e := q.DirectoryUser(ctx, id); e != nil {
			return e
		}
		return q.SetDirectoryPassword(ctx, id, hash)
	})
}

// DeleteUser anonymizes the account after verifying ownership and Home cleanup.
// The content service purges Home in the same caller-owned transaction.
func (r *Repo) DeleteUser(ctx context.Context, id string) error {
	return r.change(ctx, func(q Queries) error {
		u, e := q.DirectoryUser(ctx, id)
		if e != nil {
			return e
		}
		if u.DeletedAt != nil {
			return dberr.ErrNotFound
		}
		last, e := q.UserIsLastWorkspaceOwner(ctx, id)
		if e != nil {
			return e
		}
		if last {
			return dberr.ErrLastWorkspaceOwner
		}
		home, e := q.UserHasHome(ctx, id)
		if e != nil {
			return e
		}
		if home {
			return dberr.ErrRetained
		}
		return q.DeleteDirectoryUserData(ctx, id, time.Now().UTC())
	})
}
func (r *Repo) LastWorkspaceOwner(ctx context.Context, id string) (bool, error) {
	return r.read.UserIsLastWorkspaceOwner(ctx, id)
}
func (r *Repo) Groups(ctx context.Context) ([]Group, error) { return r.read.DirectoryGroups(ctx) }
func (r *Repo) GroupByName(ctx context.Context, name string) (Group, error) {
	gs, e := r.Groups(ctx)
	if e != nil {
		return Group{}, e
	}
	for _, g := range gs {
		if g.Name == name {
			return g, nil
		}
	}
	return Group{}, dberr.ErrNotFound
}
func (r *Repo) CreateGroup(ctx context.Context, id, name, description string, source GroupSource) (Group, error) {
	if id == "" {
		id = uuid.NewString()
	}
	if source == "" {
		source = SourceLocal
	}
	if !source.Valid() {
		return Group{}, dberr.ErrCheckViolation
	}
	g := Group{ID: id, Name: name, Description: description, Source: source, Version: 1, CreatedAt: time.Now().UTC()}
	e := r.change(ctx, func(q Queries) error { return q.InsertDirectoryGroup(ctx, g) })
	return g, e
}
func (r *Repo) SaveGroup(ctx context.Context, g Group, version int) error {
	return r.change(ctx, func(q Queries) error { ok, e := q.UpdateDirectoryGroup(ctx, g, version); return concurrent(ok, e) })
}
func (r *Repo) DeleteGroup(ctx context.Context, id string, version int) error {
	return r.change(ctx, func(q Queries) error { ok, e := q.DeleteDirectoryGroup(ctx, id, version); return concurrent(ok, e) })
}
func concurrent(ok bool, e error) error {
	if e != nil {
		return e
	}
	if !ok {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}
func validatePrincipal(ctx context.Context, q Queries, p Principal) error {
	if !p.Type.Valid() {
		return dberr.ErrCheckViolation
	}
	ok, e := q.PrincipalExists(ctx, string(p.Type), p.ID)
	if e != nil {
		return e
	}
	if !ok {
		return dberr.ErrInvalidReference
	}
	return nil
}
func (r *Repo) ValidatePrincipal(ctx context.Context, p Principal) error {
	return validatePrincipal(ctx, r.read, p)
}
func (r *Repo) Memberships(ctx context.Context, filter MembershipFilter) ([]Membership, error) {
	return r.read.DirectoryMemberships(ctx, filter)
}
func (r *Repo) Members(ctx context.Context, id string) ([]Principal, error) {
	if _, e := r.Group(ctx, id); e != nil {
		return nil, e
	}
	ms, e := r.Memberships(ctx, MembershipFilter{GroupID: id})
	out := []Principal{}
	for _, m := range ms {
		if m.GroupID == id {
			out = append(out, m.Principal)
		}
	}
	return out, e
}

// SetMember is the sole writer for local memberships. OIDC synchronization uses
// SyncMembership, which accepts only users in provider-managed groups.
func (r *Repo) SetMember(ctx context.Context, groupID string, p Principal, remove bool) error {
	return r.change(ctx, func(q Queries) error {
		g, e := q.DirectoryGroup(ctx, groupID)
		if e != nil {
			return e
		}
		if g.Source == SourceOIDC {
			return ErrManagedGroup
		}
		return setMember(ctx, q, groupID, p, remove)
	})
}
func (r *Repo) SyncMembership(ctx context.Context, groupID, subject string, remove bool) error {
	return r.change(ctx, func(q Queries) error {
		g, e := q.DirectoryGroup(ctx, groupID)
		if e != nil {
			return e
		}
		if g.Source != SourceOIDC {
			return dberr.ErrInvalidReference
		}
		return setMember(ctx, q, groupID, Principal{PrincipalUser, subject}, remove)
	})
}
func setMember(ctx context.Context, q Queries, groupID string, p Principal, remove bool) error {
	if e := validatePrincipal(ctx, q, p); e != nil {
		return e
	}
	if remove {
		return q.DeleteDirectoryMembership(ctx, groupID, p)
	}
	ms, e := q.DirectoryMemberships(ctx, MembershipFilter{})
	if e != nil {
		return e
	}
	ms = append(ms, Membership{GroupID: groupID, Principal: p})
	children := map[string][]string{}
	for _, m := range ms {
		if m.Principal.Type == PrincipalGroup {
			children[m.GroupID] = append(children[m.GroupID], m.Principal.ID)
		}
	}
	var visit func(string, map[string]bool) (int, error)
	visit = func(id string, path map[string]bool) (int, error) {
		if path[id] {
			return 0, dberr.ErrCycleDetected
		}
		path[id] = true
		defer delete(path, id)
		depth := 1
		for _, child := range children[id] {
			n, e := visit(child, path)
			if e != nil {
				return 0, e
			}
			if n+1 > depth {
				depth = n + 1
			}
		}
		if depth > MaxGroupDepth {
			return 0, fmt.Errorf("%w: nested groups exceed the maximum depth of %d", dberr.ErrCheckViolation, MaxGroupDepth)
		}
		return depth, nil
	}
	for id := range children {
		if _, e := visit(id, map[string]bool{}); e != nil {
			return e
		}
	}
	return q.InsertDirectoryMembership(ctx, Membership{ID: uuid.NewString(), GroupID: groupID, Principal: p})
}
func (r *Repo) Roles(ctx context.Context) ([]Role, error) { return r.read.DirectoryRoles(ctx) }
func (r *Repo) Role(ctx context.Context, id string) (Role, error) {
	rs, e := r.Roles(ctx)
	if e != nil {
		return Role{}, e
	}
	for _, v := range rs {
		if v.ID == id {
			return v, nil
		}
	}
	return Role{}, dberr.ErrNotFound
}
func (r *Repo) RoleExists(ctx context.Context, id string) (bool, error) {
	_, e := r.Role(ctx, id)
	if dberr.IsNotFound(e) {
		return false, nil
	}
	return e == nil, e
}
func (r *Repo) CreateRole(ctx context.Context, role Role) (Role, error) {
	if role.ID == "" {
		role.ID = uuid.NewString()
	}
	if role.Scopes == nil {
		role.Scopes = []string{}
	}
	role.Version = 1
	e := r.change(ctx, func(q Queries) error { return q.InsertDirectoryRole(ctx, role) })
	return role, e
}
func (r *Repo) SaveRole(ctx context.Context, role Role, version int) error {
	return r.change(ctx, func(q Queries) error { ok, e := q.UpdateDirectoryRole(ctx, role, version); return concurrent(ok, e) })
}
func (r *Repo) DeleteRole(ctx context.Context, id string, version int) error {
	return r.change(ctx, func(q Queries) error { ok, e := q.DeleteDirectoryRole(ctx, id, version); return concurrent(ok, e) })
}
func (r *Repo) Assignments(ctx context.Context, id string) ([]Principal, error) {
	if _, e := r.Role(ctx, id); e != nil {
		return nil, e
	}
	return r.read.RolePrincipals(ctx, id)
}
func (r *Repo) SetAssignment(ctx context.Context, id string, p Principal, remove bool) error {
	return r.change(ctx, func(q Queries) error {
		if e := validatePrincipal(ctx, q, p); e != nil {
			return e
		}
		roles, err := q.DirectoryRoles(ctx)
		if err != nil {
			return err
		}
		exists := false
		for _, role := range roles {
			if role.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			return dberr.ErrNotFound
		}
		if remove {
			return q.DeleteDirectoryAssignment(ctx, id, p)
		}
		return q.InsertDirectoryAssignment(ctx, id, p)
	})
}
func (r *Repo) Organization(ctx context.Context) (Organization, error) {
	return r.read.DirectoryOrganization(ctx)
}
func (r *Repo) EnsureOrganization(ctx context.Context) error {
	return r.change(ctx, func(q Queries) error { return q.InsertDirectoryOrganization(ctx) })
}
func (r *Repo) SaveOrganization(ctx context.Context, o Organization, version int64) error {
	return r.change(ctx, func(q Queries) error {
		ok, e := q.UpdateDirectoryOrganization(ctx, o, version)
		return concurrent(ok, e)
	})
}
func (r *Repo) Preferences(ctx context.Context, owner PreferenceOwner, id string) (Preferences, error) {
	p, e := r.read.DirectoryPreferences(ctx, owner, id)
	if dberr.IsNotFound(e) {
		return Preferences{Values: map[string]string{}}, nil
	}
	return p, e
}
func (r *Repo) SavePreferences(ctx context.Context, owner PreferenceOwner, id string, version int64, values map[string]string) error {
	return r.change(ctx, func(q Queries) error {
		if !owner.Valid() || owner == OwnerOrganization && id != "" {
			return dberr.ErrInvalidReference
		}
		if owner == OwnerUser {
			if _, e := q.DirectoryUser(ctx, id); e != nil {
				return e
			}
		}
		ok, e := q.WriteDirectoryPreferences(ctx, owner, id, version, values)
		if errors.Is(e, dberr.ErrUniqueViolation) {
			return dberr.ErrConcurrentUpdate
		}
		return concurrent(ok, e)
	})
}

// AccessTokens lists tokens. Live tokens are unexpired at f.Now, which
// defaults to the current time.
func (r *Repo) AccessTokens(ctx context.Context, f AccessTokenFilter) ([]AccessToken, error) {
	if f.Live && f.Now.IsZero() {
		f.Now = time.Now().UTC()
	}
	return r.read.DirectoryAccessTokens(ctx, f)
}
func (r *Repo) CreateAccessToken(ctx context.Context, t AccessToken) (AccessToken, error) {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if len(t.Scopes) == 0 {
		return AccessToken{}, dberr.ErrCheckViolation
	}
	if t.Resources == nil {
		t.Resources = []string{}
	}
	e := r.change(ctx, func(q Queries) error { return q.InsertDirectoryAccessToken(ctx, t) })
	return t, e
}
func (r *Repo) RevokeAccessToken(ctx context.Context, id, subject string, admin bool) error {
	return r.change(ctx, func(q Queries) error {
		ok, e := q.RevokeDirectoryAccessToken(ctx, id, subject, admin, time.Now().UTC())
		if e != nil {
			return e
		}
		if !ok {
			return dberr.ErrNotFound
		}
		return nil
	})
}
func (r *Repo) TouchAccessToken(ctx context.Context, id string, now time.Time) error {
	return r.change(ctx, func(q Queries) error { return q.TouchDirectoryAccessToken(ctx, id, now) })
}
