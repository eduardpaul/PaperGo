package dms

import (
	"errors"
	"papergo/ent/collectiontype"
	"papergo/ent/contenttype"
	"papergo/ent/resource"
	"papergo/internal/identity"
	"sync"
	"testing"
)

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
		row, err := s.Client.Resource.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Delete(ctx, "alice", id, row.Version, DeleteOptions{Recursive: true}); !errors.Is(err, ErrProtectedHome) {
			t.Fatal("protected delete", err)
		}
	}
	for _, id := range []string{home.DocumentsLibraryID, home.InboxLibraryID} {
		lib, err := s.Client.Resource.Get(ctx, id)
		if err != nil || lib.Versioning != resource.VersioningMajor || lib.PublishingEnabled {
			t.Fatal(lib, err)
		}
		attached, err := s.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(id), collectiontype.IsDefaultEQ(true)).Only(ctx)
		if err != nil {
			t.Fatal(err)
		}
		typ, err := s.Client.ContentType.Get(ctx, attached.ContentTypeID)
		if err != nil || typ.Key != "document" {
			t.Fatal(typ, err)
		}
	}
	if n, err := s.Client.ContentType.Query().Where(contenttype.TemplateKeyEQ("document")).Count(ctx); err != nil || n != 1 {
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
	token, err := s.Directory().CreateRESTToken(ctx, "bob", identity.RESTTokenInput{Name: "Home discovery", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	limited := identity.WithRESTToken(ctx, identity.RESTTokenIdentity{ID: token.Token.ID, Subject: "bob", Scopes: []string{"list.read"}})
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
	if _, err = s.db.ExecContext(ctx, "CREATE TRIGGER fail_home BEFORE INSERT ON list_views WHEN new.name='All documents' BEGIN SELECT RAISE(ABORT,'forced');END;"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Home(ctx, "alice"); err == nil {
		t.Fatal("forced provisioning succeeded")
	}
	if n, err := s.Client.Resource.Query().Where(resource.PersonalOwnerIDEQ("alice")).Count(ctx); err != nil || n != 0 {
		t.Fatal("partial Home", n, err)
	}
}
