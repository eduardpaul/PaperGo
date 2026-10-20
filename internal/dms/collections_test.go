package dms

import (
	"errors"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"strings"
	"testing"
)

func TestCollectionMetadataFolderPolicyAndPermissions(t *testing.T) {
	s, workspace, _ := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", workspace.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(testContext, "bob", workspace.ID, CreateResource{Kind: "list", Name: "Denied"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("member created collection", err)
	}
	for _, kind := range []string{"list", "library"} {
		collection, err := s.Create(testContext, "alice", workspace.ID, CreateResource{Kind: kind, Name: " Records ", Description: "Team records", AllowFolders: ptr(false)})
		if err != nil || collection.Name != "Records" || collection.Description != "Team records" || collection.AllowFolders {
			t.Fatal(collection, err)
		}
		if _, err = s.Create(testContext, "bob", collection.ID, CreateResource{Kind: "folder", Name: "Denied"}); err == nil {
			t.Fatal("created folder in flat collection")
		}
		if _, err = s.Create(testContext, "bob", collection.ID, CreateResource{Kind: "item", Name: "Allowed"}); err != nil {
			t.Fatal("flat collection blocks items", err)
		}
		name, description := " Renamed ", "Updated"
		if _, err = s.Update(testContext, "bob", collection.ID, latest(t, s, collection.ID).Version, UpdateResource{Name: &name}); !errors.Is(err, ErrForbidden) {
			t.Fatal("member renamed collection", err)
		}
		collection, err = s.Update(testContext, "alice", collection.ID, latest(t, s, collection.ID).Version, UpdateResource{Name: &name, Description: &description, AllowFolders: ptr(true)})
		if err != nil || collection.Name != "Renamed" || collection.Description != description || !collection.AllowFolders {
			t.Fatal(collection, err)
		}
		folder := create(t, s, collection.ID, "folder", "Folder", nil)
		collection, err = s.Update(testContext, "alice", collection.ID, collection.Version, UpdateResource{AllowFolders: ptr(false)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Create(testContext, "bob", folder.ID, CreateResource{Kind: "folder", Name: "Nested"}); err == nil {
			t.Fatal("nested folder bypassed collection policy")
		}
		if _, err = s.Create(testContext, "bob", folder.ID, CreateResource{Kind: "item", Name: "Existing folder item"}); err != nil {
			t.Fatal("existing folders must remain usable", err)
		}
		if _, err = s.DAL.Resources().Create(testContext, resources.New{WorkspaceID: workspace.ID, ParentID: collection.ID, ContainerID: collection.ID, Kind: refs.KindFolder, Name: "Raw bypass"}); err == nil {
			t.Fatal("storage bypassed folder policy")
		}
	}
	if _, err := s.Create(testContext, "alice", workspace.ID, CreateResource{Kind: "list", Name: strings.Repeat("🙂", 101)}); err == nil {
		t.Fatal("collection name exceeded UTF-16 limit")
	}
	if _, err := s.Create(testContext, "alice", workspace.ID, CreateResource{Kind: "list", Name: "Long description", Description: strings.Repeat("🙂", 1001)}); err == nil {
		t.Fatal("description exceeded UTF-16 limit")
	}
}

func TestCollectionDeletionRemovesLiveContentAndRetainsHistory(t *testing.T) {
	s, workspace, collection := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", workspace.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	a := create(t, s, collection.ID, "item", "A", nil)
	folder := create(t, s, collection.ID, "folder", "Folder", nil)
	b := create(t, s, folder.ID, "item", "B", nil)
	typ := referencesType(t, s, workspace.ID)
	edge, err := s.Link(testContext, "alice", a.ID, CreateRelationship{Type: typ.ID, TargetID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "bob", collection.ID, latest(t, s, collection.ID).Version, DeleteOptions{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("member deleted collection", err)
	}
	collection, err = s.SetPermissions(testContext, "alice", collection.ID, latest(t, s, collection.ID).Version, Permissions{Grants: []Permission{{"user", "bob", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(testContext, "alice", folder.ID, folder.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(testContext, "bob", collection.ID, collection.Version-1, DeleteOptions{}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale delete accepted", err)
	}
	if err = s.Delete(testContext, "bob", collection.ID, collection.Version, DeleteOptions{}); err != nil {
		t.Fatal("manager could not delete exclusive descendants", err)
	}
	for _, id := range []string{collection.ID, folder.ID, a.ID, b.ID} {
		if _, err = s.Get(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted resource visible", id, err)
		}
	}
	if _, err = s.DAL.Relationships().Find(testContext, edge.ID); err != nil {
		t.Fatal("recovery edge lost", err)
	}
	if n, err := countRevisions(s, ""); err != nil || n != 3 {
		t.Fatal("revision history lost", n, err)
	}
	if n, err := countHistory(s); err != nil || n != 2 {
		t.Fatal("folder snapshots must not create visible history", n, err)
	}
	if n, err := countSurfaces(s, surfaces.Filter{}); err != nil || n != 0 {
		t.Fatal("derived content survived", n, err)
	}
}
