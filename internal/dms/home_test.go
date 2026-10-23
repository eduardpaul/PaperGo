package dms

import (
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/grants"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"sync"
	"testing"
)

func TestHomeIdentityAndMembershipDatabaseConstraints(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := t.Context()
	home, err := s.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := s.DAL.Resources().Resource(ctx, home.InboxLibraryID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DAL.Resources().Change(ctx, inbox.ID, inbox.Version, "alice", resources.Changes{ParentID: &w.ID}); err == nil {
		t.Fatal("system library moved out of Home")
	}
	if _, err = s.DAL.Grants().Create(ctx, grants.Grant{ResourceID: home.WorkspaceID, PrincipalType: grants.PrincipalUser, PrincipalID: "bob", Action: grants.ActionManage}); err == nil {
		t.Fatal("extra Home owner inserted")
	}
	owners, err := s.DAL.Grants().List(ctx, grants.Filter{ResourceID: home.WorkspaceID})
	if err != nil || len(owners) != 1 {
		t.Fatal(owners, err)
	}
	if _, err = s.DAL.Grants().ChangeAction(ctx, owners[0].ID, grants.ActionRead); err == nil {
		t.Fatal("Home owner demoted")
	}
	if err = s.DAL.Grants().Revoke(ctx, owners[0].ID); err == nil {
		t.Fatal("Home owner removed")
	}
	if _, err = s.DAL.Resources().Create(ctx, resources.New{WorkspaceID: w.ID, ParentID: w.ID, Kind: refs.KindLibrary, Name: "Wrong workspace", SystemKey: resources.SystemInbox}); err == nil {
		t.Fatal("system library outside Home accepted")
	}
	if _, err = s.DAL.Resources().Create(ctx, resources.New{ID: "00000000-0000-4000-8000-000000000099", Kind: refs.KindWorkspace, Name: "Another Home", PersonalOwnerID: "alice"}); err == nil {
		t.Fatal("second Home accepted")
	}
	if _, err = s.DAL.Directory().BindInbox(ctx, "missing group", home.InboxLibraryID); err == nil {
		t.Fatal("unknown group accepted")
	}
}

func TestHomeConcurrentProvisioningAndProtection(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	results := make(chan Home, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			h, err := s.Home(ctx, "alice")
			if err != nil {
				failures <- err
			} else {
				results <- h
			}
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var home Home
	for h := range results {
		if home.WorkspaceID != "" && home != h {
			t.Fatal("different Homes", home, h)
		}
		home = h
	}
	for _, id := range []string{home.WorkspaceID, home.DocumentsLibraryID, home.InboxLibraryID} {
		row, err := s.DAL.Resources().Resource(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Delete(ctx, "alice", id, row.Version, DeleteOptions{Recursive: true}); !errors.Is(err, ErrProtectedHome) {
			t.Fatal("protected delete", err)
		}
	}
	for _, id := range []string{home.DocumentsLibraryID, home.InboxLibraryID} {
		lib, err := s.DAL.Resources().Resource(ctx, id)
		if err != nil || lib.Versioning != resources.VersioningOff || lib.PublishingEnabled {
			t.Fatal(lib, err)
		}
		typ, err := s.DAL.Catalog().CollectionContentType(ctx, id, "")
		if err != nil || typ.Key != "document" {
			t.Fatal(typ, err)
		}
	}
	if n, err := s.DAL.Catalog().CountContentTypes(ctx, catalog.ContentTypeFilter{TemplateKey: "document"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.SetWorkspaceMember(ctx, "alice", home.WorkspaceID, WorkspaceMember{Subject: "bob", Role: "owner"}); !errors.Is(err, ErrProtectedHome) {
		t.Fatal(err)
	}
	if err := s.RemoveWorkspaceMember(ctx, "organization-admin", home.WorkspaceID, "alice"); !errors.Is(err, ErrProtectedHome) {
		t.Fatal(err)
	}
	bob, err := s.Home(ctx, "bob")
	if err != nil || bob.WorkspaceID == home.WorkspaceID {
		t.Fatal(bob, err)
	}
	if _, err = s.Home(ctx, "missing"); !errors.Is(err, ErrForbidden) {
		t.Fatal("unknown user", err)
	}
	if _, err = s.Directory().SetUserActive(ctx, "organization-admin", "bob", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Home(ctx, "bob"); !errors.Is(err, ErrForbidden) {
		t.Fatal("inactive user", err)
	}
}

func TestHomeRestrictedTokenAndRollback(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := t.Context()
	token, err := s.Directory().CreateAccessToken(ctx, "bob", identity.AccessTokenInput{Name: "Home discovery", Scopes: []string{"item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	limited := identity.WithAccessToken(ctx, identity.AccessTokenIdentity{ID: token.Token.ID, Subject: "bob", Scopes: []string{"item.read"}})
	home, err := s.Home(limited, "bob")
	if err != nil || home.InboxLibraryID == "" {
		t.Fatal(home, err)
	}
	if _, err = s.Create(limited, "bob", "", CreateResource{Kind: "workspace", Name: "Other"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("general creation allowed", err)
	}
	if _, err = s.InboxUploadDestination(limited, "bob", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("upload allowed", err)
	}
	// A late provisioning failure must leave neither Home nor a partial library.
	daltest.Reject(t, s.DAL, "list_views", daltest.OnInsert, daltest.Row{"name": "All documents"})
	if _, err = s.Home(ctx, "alice"); err == nil {
		t.Fatal("forced provisioning succeeded")
	}
	if n, err := countResources(s, resources.Filter{PersonalOwnerID: "alice"}); err != nil || n != 0 {
		t.Fatal("partial Home", n, err)
	}
}
