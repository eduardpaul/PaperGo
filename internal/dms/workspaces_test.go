package dms

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"papergo/ent/grant"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
)

func TestWorkspaceDescriptionsMembershipAndOwnerControl(t *testing.T) {
	s, w, list := fixture(t)
	description := "Finance documents"
	w, err := s.Update(testContext, "alice", w.ID, w.Version, UpdateResource{Description: &description})
	if err != nil || w.Description != description {
		t.Fatalf("description: %+v %v", w, err)
	}
	for _, m := range []WorkspaceMember{{"bob", "member"}, {"carol", "visitor"}} {
		if err = s.SetWorkspaceMember(testContext, "alice", w.ID, m); err != nil {
			t.Fatal(err)
		}
	}
	members, err := s.WorkspaceMembers(testContext, "carol", w.ID)
	if err != nil || len(members) != 3 || members[0] != (WorkspaceMember{"alice", "owner"}) {
		t.Fatalf("members: %v %v", members, err)
	}
	if err = s.SetWorkspaceMember(testContext, "bob", w.ID, WorkspaceMember{"dave", "member"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-owner changed membership", err)
	}
	if _, err = s.Update(testContext, "bob", w.ID, w.Version, UpdateResource{Name: &w.Name}); !errors.Is(err, ErrNotFound) {
		t.Fatal("member renamed workspace", err)
	}
	if err = s.Delete(testContext, "carol", w.ID, w.Version); !errors.Is(err, ErrNotFound) {
		t.Fatal("visitor deleted workspace", err)
	}
	if _, err = s.Create(testContext, "bob", list.ID, CreateResource{Kind: "item", Name: "Contribution"}); err != nil {
		t.Fatal("member cannot contribute", err)
	}
	if _, err = s.Create(testContext, "carol", list.ID, CreateResource{Kind: "item", Name: "Forbidden"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("visitor can contribute", err)
	}
	page, err := s.Browse(testContext, "carol", Browse{})
	if err != nil || len(page.Data) != 1 {
		t.Fatal("member cannot discover workspace", page, err)
	}
	if err = s.RemoveWorkspaceMember(testContext, "alice", w.ID, "carol"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "carol", w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed member sees workspace", err)
	}
	if _, err = s.WorkspaceMembers(testContext, "carol", w.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("membership leaked", err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{"bob", "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(testContext, "bob", list.ID, CreateResource{Kind: "item", Name: "Revoked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("demoted member can write", err)
	}
	// Membership updates retain the metadata ETag and retries create no extra rows.
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{"bob", "visitor"}); err != nil {
		t.Fatal(err)
	}
	current := latest(t, s, w.ID)
	if current.Version != w.Version {
		t.Fatal("membership consumed metadata ETag", current.Version)
	}
	if _, err = s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}}}); err == nil {
		t.Fatal("workspace ACL replacement bypasses member API")
	}
	// Even an exclusive scope with no grants remains manageable by workspace owners.
	list, err = s.SetPermissions(testContext, "alice", list.ID, list.Version, Permissions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "alice", list.ID); err != nil {
		t.Fatal("owner locked out", err)
	}
	if _, err = s.Get(testContext, "bob", list.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("visitor bypassed exclusive scope", err)
	}
}

func TestWorkspaceUnicodeMetadataLimits(t *testing.T) {
	s, _, _ := fixture(t)
	w, err := s.Create(testContext, "alice", "", CreateResource{Kind: "workspace", Name: strings.Repeat("界", 200), Description: strings.Repeat("界", 2000)})
	if err != nil {
		t.Fatal("valid Unicode metadata rejected", err)
	}
	if w.Description != strings.Repeat("界", 2000) {
		t.Fatal("description truncated")
	}
	if _, err = s.Update(testContext, "alice", w.ID, w.Version, UpdateResource{Description: ptr(strings.Repeat("界", 2001))}); err == nil {
		t.Fatal("oversized Unicode description accepted")
	}
	for _, in := range []CreateResource{{Kind: "workspace", Name: strings.Repeat("😀", 101)}, {Kind: "workspace", Name: "Team", Description: strings.Repeat("😀", 1001)}} {
		if _, err = s.Create(testContext, "alice", "", in); err == nil {
			t.Fatal("UTF-16 metadata limit ignored")
		}
	}
}

func TestWorkspaceLastOwnerIsProtectedAndTransferable(t *testing.T) {
	s, w, list := fixture(t)
	if err := s.RemoveWorkspaceMember(testContext, "alice", w.ID, "alice"); !errors.Is(err, ErrLastOwner) {
		t.Fatal("last owner removed", err)
	}
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{"alice", "visitor"}); !errors.Is(err, ErrLastOwner) {
		t.Fatal("last owner demoted", err)
	}
	if _, err := s.Client.Grant.Delete().Where(grant.ResourceIDEQ(w.ID), grant.SubjectEQ("alice")).Exec(testContext); err == nil {
		t.Fatal("database allowed last owner deletion")
	}
	if _, err := s.Client.Grant.Update().Where(grant.ResourceIDEQ(w.ID), grant.SubjectEQ("alice")).SetAction(grant.ActionRead).Save(testContext); err == nil {
		t.Fatal("database allowed last owner demotion")
	}
	if _, err := s.Client.ExecContext(testContext, `UPDATE grants SET resource_id=? WHERE resource_id=? AND subject='alice'`, list.ID, w.ID); err == nil {
		t.Fatal("database allowed moving the workspace owner grant")
	}
	if _, err := s.Client.Grant.Create().SetResourceID(w.ID).SetSubject("alice").SetAction(grant.ActionRead).Save(testContext); err == nil {
		t.Fatal("database allowed two roles for the same workspace member")
	}
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{"bob", "owner"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, member := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(member string) { defer wg.Done(); _ = s.RemoveWorkspaceMember(testContext, "alice", w.ID, member) }(member)
	}
	wg.Wait()
	n, err := s.Client.Grant.Query().Where(grant.ResourceIDEQ(w.ID), grant.ActionEQ(grant.ActionManage)).Count(testContext)
	if err != nil || n != 1 {
		t.Fatal("concurrent removal lost the owner", n, err)
	}
}

func TestWorkspaceValidationAndDeletionCoversExclusiveDescendants(t *testing.T) {
	s, w, list := fixture(t)
	for _, in := range []CreateResource{{Kind: "workspace", Name: strings.Repeat("x", 201)}, {Kind: "workspace", Name: "Valid", Description: strings.Repeat("x", 2001)}} {
		if _, err := s.Create(testContext, "alice", "", in); err == nil {
			t.Fatal("invalid workspace accepted")
		}
	}
	if _, err := s.Update(testContext, "alice", list.ID, list.Version, UpdateResource{Description: ptr("Invalid")}); err == nil {
		t.Fatal("list accepted workspace description")
	}
	item := create(t, s, list.ID, "item", "Retained history", nil)
	if _, err := s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "alice", w.ID, w.Version+1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale deletion accepted", err)
	}
	if err := s.Delete(testContext, "alice", w.ID, w.Version); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{w.ID, list.ID, item.ID} {
		if _, err := s.Get(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
			t.Fatal("deleted workspace content visible", id, err)
		}
	}
	n, err := s.Client.Resource.Query().Where(resource.WorkspaceIDEQ(w.ID), resource.DeletedAtIsNil()).Count(testContext)
	if err != nil || n != 0 {
		t.Fatal("live workspace descendants", n, err)
	}
	n, err = s.Client.ItemSurface.Query().Where(itemsurface.WorkspaceIDEQ(w.ID)).Count(testContext)
	if err != nil || n != 0 {
		t.Fatal("workspace search projections retained", n, err)
	}
	if _, err = s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Orphan"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted workspace can create content", err)
	}
}
