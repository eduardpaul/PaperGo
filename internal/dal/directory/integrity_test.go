package directory_test

import (
	"context"
	"errors"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"sync"
	"testing"
)

func TestNestedGroupCyclesAndWorkspaceOwnerInvariants(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		gA, err := s.Directory().CreateGroup(ctx, "grp-a", "Group A", "", directory.SourceLocal)
		if err != nil {
			t.Fatalf("CreateGroup A: %v", err)
		}
		gB, err := s.Directory().CreateGroup(ctx, "grp-b", "Group B", "", directory.SourceLocal)
		if err != nil {
			t.Fatalf("CreateGroup B: %v", err)
		}
		gC, err := s.Directory().CreateGroup(ctx, "grp-c", "Group C", "", directory.SourceLocal)
		if err != nil {
			t.Fatalf("CreateGroup C: %v", err)
		}

		// A -> B -> C
		if err := s.Directory().SetMember(ctx, gA.ID, directory.Principal{Type: directory.PrincipalGroup, ID: gB.ID}, false); err != nil {
			t.Fatalf("AddGroupMembership A->B: %v", err)
		}
		if err := s.Directory().SetMember(ctx, gB.ID, directory.Principal{Type: directory.PrincipalGroup, ID: gC.ID}, false); err != nil {
			t.Fatalf("AddGroupMembership B->C: %v", err)
		}

		// C -> A must fail with ErrCycleDetected
		err = s.Directory().SetMember(ctx, gC.ID, directory.Principal{Type: directory.PrincipalGroup, ID: gA.ID}, false)
		if !errors.Is(err, dberr.ErrCycleDetected) {
			t.Fatalf("expected ErrCycleDetected on group cycle C->A, got %v", err)
		}

		// Workspace last-owner invariant
		u1, _ := s.Directory().CreateUser(ctx, "u-owner-1", "Owner 1", "o1@example.com", true, false)
		u2, _ := s.Directory().CreateUser(ctx, "u-owner-2", "Owner 2", "o2@example.com", true, false)

		ws, err := s.Resources().Create(ctx, resources.New{
			ID:          "ws-owners",
			WorkspaceID: "ws-owners",
			Kind:        refs.KindWorkspace,
			Name:        "Shared Workspace",
			CreatedBy:   u1.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource ws: %v", err)
		}

		g1, err := s.Grants().Create(ctx, grants.Grant{ID: "grant-1", ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: u1.ID, Action: grants.ActionManage})
		if err != nil {
			t.Fatalf("CreateGrant owner 1: %v", err)
		}

		// Demoting or revoking the sole owner must fail with ErrLastWorkspaceOwner
		if _, err := s.Grants().ChangeAction(ctx, g1.ID, grants.ActionWrite); !errors.Is(err, dberr.ErrLastWorkspaceOwner) {
			t.Fatalf("expected ErrLastWorkspaceOwner on demoting sole owner, got %v", err)
		}
		if err := s.Grants().Revoke(ctx, g1.ID); !errors.Is(err, dberr.ErrLastWorkspaceOwner) {
			t.Fatalf("expected ErrLastWorkspaceOwner on revoking sole owner, got %v", err)
		}

		// Adding a second owner allows revoking the first
		if _, err := s.Grants().Create(ctx, grants.Grant{ID: "grant-2", ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: u2.ID, Action: grants.ActionManage}); err != nil {
			t.Fatalf("CreateGrant owner 2: %v", err)
		}
		if err := s.Grants().Revoke(ctx, g1.ID); err != nil {
			t.Fatalf("RevokeGrant owner 1 after adding owner 2: %v", err)
		}

		// Unified access_tokens (REST API & WebDAV credentials)
		tok, err := s.Directory().CreateAccessToken(ctx, directory.AccessToken{ID: "tok-1", Subject: u1.ID, Name: "MacBook WebDAV & CLI", Prefix: "pgo_live_", SecretHash: "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", CreatedBy: u1.ID, Scopes: []string{"files:read", "files:write", "webdav"}})
		if err != nil {
			t.Fatalf("CreateAccessToken: %v", err)
		}
		if len(tok.Scopes) != 3 || tok.RevokedAt != nil {
			t.Fatalf("unexpected access token state: %+v", tok)
		}
		if err := s.Directory().RevokeAccessToken(ctx, tok.ID, "", true); err != nil {
			t.Fatalf("RevokeAccessToken: %v", err)
		}
	})
}

func TestConcurrentDirectoryMutations(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		for _, id := range []string{"left", "right"} {
			if _, err := s.Directory().CreateGroup(ctx, id, id, "", directory.SourceLocal); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Go(func() {
			errs[0] = s.Directory().SetMember(ctx, "left", directory.Principal{Type: directory.PrincipalGroup, ID: "right"}, false)
		})
		wg.Go(func() {
			errs[1] = s.Directory().SetMember(ctx, "right", directory.Principal{Type: directory.PrincipalGroup, ID: "left"}, false)
		})
		wg.Wait()
		daltest.AssertOneRejected(t, errs, dberr.ErrCycleDetected)
		for _, id := range []string{"alice", "bob"} {
			if _, err := s.Directory().CreateUser(ctx, id, id, "", true, false); err != nil {
				t.Fatal(err)
			}
		}
		ws, err := s.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Workspace"})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"alice", "bob"} {
			if _, err := s.Grants().Create(ctx, grants.Grant{ID: id, ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: id, Action: grants.ActionManage}); err != nil {
				t.Fatal(err)
			}
		}
		wg.Go(func() { errs[0] = s.Grants().Revoke(ctx, "alice") })
		wg.Go(func() { errs[1] = s.Grants().Revoke(ctx, "bob") })
		wg.Wait()
		daltest.AssertOneRejected(t, errs, dberr.ErrLastWorkspaceOwner)
	})
}

func TestDeleteUserOwnershipAndCleanup(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		for _, id := range []string{"alice", "bob", "inactive"} {
			if _, err := s.Directory().CreateUser(ctx, id, id, "", id != "inactive", false); err != nil {
				t.Fatal(err)
			}
		}
		ws, err := s.Resources().Create(ctx, resources.New{ID: "workspace", Kind: refs.KindWorkspace, Name: "Workspace"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Grants().Create(ctx, grants.Grant{ID: "alice-grant", ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: "alice", Action: grants.ActionManage}); err != nil {
			t.Fatal(err)
		}
		if err := s.Directory().SavePreferences(ctx, directory.OwnerUser, "alice", 0, map[string]string{}); err != nil {
			t.Fatal(err)
		}
		if err := s.Directory().DeleteUser(ctx, "alice"); !errors.Is(err, dberr.ErrLastWorkspaceOwner) {
			t.Fatalf("delete last owner: %v", err)
		}
		if _, err := s.Grants().Create(ctx, grants.Grant{ID: "", ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: "inactive", Action: grants.ActionManage}); !errors.Is(err, dberr.ErrInvalidPermissionScope) {
			t.Fatalf("inactive owner accepted: %v", err)
		}
		if _, err := s.Grants().Create(ctx, grants.Grant{ID: "", ResourceID: ws.ID, PrincipalType: grants.PrincipalUser, PrincipalID: "bob", Action: grants.ActionManage}); err != nil {
			t.Fatal(err)
		}
		if err := s.Directory().DeleteUser(ctx, "alice"); err != nil {
			t.Fatal(err)
		}
		kept, err := s.Grants().List(ctx, grants.Filter{PrincipalID: "alice"})
		if err != nil || len(kept) != 0 {
			t.Fatalf("deleted user kept grant: %+v, %v", kept, err)
		}
		prefs, err := s.Directory().Preferences(ctx, directory.OwnerUser, "alice")
		if err != nil || prefs.Version != 0 {
			t.Fatalf("deleted user kept preferences: %+v, %v", prefs, err)
		}
	})
}
