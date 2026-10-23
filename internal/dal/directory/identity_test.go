package directory_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"reflect"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/grants"
)

func TestDirectoryIdentity_SQLite(t *testing.T)   { testDirectoryIdentity(t, daltest.SQLiteStore(t)) }
func TestDirectoryIdentity_Postgres(t *testing.T) { testDirectoryIdentity(t, daltest.PostgresStore(t)) }
func testDirectoryIdentity(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	r := s.Directory()
	f := daltest.NewFixture(t, s)
	if _, err := r.CreateUser(ctx, "alice", "Alice", "", true, false); err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	org, err := r.Organization(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EnsureOrganization(ctx); err != nil {
		t.Fatal(err)
	}
	same, err := r.Organization(ctx)
	if err != nil || same.ID != org.ID {
		t.Fatalf("organization singleton: %+v %v", same, err)
	}
	child, err := r.CreateGroup(ctx, "child", "Child", "", directory.SourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.CreateGroup(ctx, "parent", "Parent", "", directory.SourceLocal)
	if err != nil {
		t.Fatal(err)
	}
	user := directory.Principal{Type: directory.PrincipalUser, ID: "alice"}
	if err := r.SetMember(ctx, child.ID, user, false); err != nil {
		t.Fatal(err)
	}
	childPrincipal := directory.Principal{Type: directory.PrincipalGroup, ID: child.ID}
	if err := r.SetMember(ctx, parent.ID, childPrincipal, false); err != nil {
		t.Fatal(err)
	}
	role, err := r.CreateRole(ctx, directory.Role{ID: "admin", Name: "Administrator", Builtin: true, Scopes: []string{"role.manage", "item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetAssignment(ctx, role.ID, directory.Principal{Type: directory.PrincipalGroup, ID: parent.ID}, false); err != nil {
		t.Fatal(err)
	}
	scopes, err := r.EffectiveScopes(ctx, "alice", "")
	if err != nil || !reflect.DeepEqual(scopes, []string{"item.read", "role.manage"}) {
		t.Fatalf("nested scopes: %v %v", scopes, err)
	}
	if has, err := r.HasAdministrator(ctx, role.ID); err != nil || !has {
		t.Fatalf("nested administrator: %v %v", has, err)
	}
	if err := r.SaveRole(ctx, directory.Role{ID: role.ID, Name: "Changed", Scopes: []string{}}, role.Version); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("changed builtin: %v", err)
	}
	if err := r.DeleteRole(ctx, role.ID, role.Version); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("deleted builtin: %v", err)
	}
	source, err := s.Resources().Resource(ctx, f.Source)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Resources().SetInheritPermissions(ctx, source.ID, source.Version, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grants().Create(ctx, grants.Grant{ResourceID: f.Source, PrincipalType: grants.PrincipalGroup, PrincipalID: child.ID, Action: grants.ActionRead}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAssignment(ctx, role.ID, childPrincipal, false); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteGroup(ctx, child.ID, child.Version+1); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := r.DeleteGroup(ctx, child.ID, child.Version); err != nil {
		t.Fatal(err)
	}
	members, err := r.Members(ctx, parent.ID)
	if err != nil || len(members) != 0 {
		t.Fatalf("deleted group kept membership: %v %v", members, err)
	}
	ps, err := r.Assignments(ctx, role.ID)
	if err != nil || len(ps) != 1 || ps[0].ID != parent.ID {
		t.Fatalf("deleted group kept assignment: %v %v", ps, err)
	}
	gs, err := s.Grants().List(ctx, grants.Filter{PrincipalType: grants.PrincipalGroup, PrincipalID: child.ID})
	if err != nil || len(gs) != 0 {
		t.Fatalf("deleted group kept grants: %v %v", gs, err)
	}
	if has, err := r.HasAdministrator(ctx, role.ID); err != nil || has {
		t.Fatalf("removed membership kept administrator: %v %v", has, err)
	}
	oidc, err := r.CreateGroup(ctx, "oidc", "Provider group", "", directory.SourceOIDC)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetMember(ctx, oidc.ID, user, false); !errors.Is(err, directory.ErrManagedGroup) {
		t.Fatalf("manual provider membership: %v", err)
	}
	if err := r.SyncMembership(ctx, oidc.ID, user.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncMembership(ctx, parent.ID, user.ID, false); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("synced local membership: %v", err)
	}
	if _, err := r.CreateUser(ctx, "service", "Service", "", true, true); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().UTC().Add(time.Hour)
	token, err := r.CreateAccessToken(ctx, directory.AccessToken{Subject: "service", Name: "Integration", SecretHash: strings.Repeat("a", 64), Integration: true, Scopes: []string{"document.read"}, ExpiresAt: &expiry})
	if err != nil {
		t.Fatal(err)
	}
	scopes, err = r.EffectiveScopes(ctx, "service", token.ID)
	if err != nil || !reflect.DeepEqual(scopes, token.Scopes) {
		t.Fatalf("integration scopes: %v %v", scopes, err)
	}
	expired := time.Now().UTC().Add(-time.Hour)
	if _, err = r.CreateAccessToken(ctx, directory.AccessToken{Subject: "service", Name: "Expired", SecretHash: strings.Repeat("b", 64), Integration: true, Scopes: []string{"document.read"}, ExpiresAt: &expired}); err != nil {
		t.Fatal(err)
	}
	live, err := r.AccessTokens(ctx, directory.AccessTokenFilter{Subject: "service", Live: true})
	if err != nil || len(live) != 1 || live[0].ID != token.ID {
		t.Fatalf("live tokens without a time: %+v %v", live, err)
	}
	if err := r.RevokeAccessToken(ctx, token.ID, "alice", false); !dberr.IsNotFound(err) {
		t.Fatalf("revoked someone else's integration: %v", err)
	}
	if err := r.RevokeAccessToken(ctx, token.ID, "", true); err != nil {
		t.Fatal(err)
	}
	scopes, err = r.EffectiveScopes(ctx, "service", token.ID)
	if err != nil || len(scopes) != 0 {
		t.Fatalf("revoked integration retained scopes: %v %v", scopes, err)
	}
}
