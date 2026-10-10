package dms

import (
	"context"
	"errors"
	"papergo/internal/identity"
	"testing"
)

func TestGroupACLInheritanceCopyResetAndNamespace(t *testing.T) {
	s, w, list := fixture(t)
	ctx := testContext
	d := s.Directory()
	parent, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Editors"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Invoice editors"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", parent.ID, identity.Principal{Type: "group", ID: child.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", child.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	list, err = s.SetPermissions(ctx, "alice", list.ID, list.Version, Permissions{Grants: []Permission{{"group", parent.ID, "write", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, list.ID, "folder", "Copied", nil)
	item := create(t, s, folder.ID, "item", "Draft", nil)
	private := create(t, s, folder.ID, "item", "Private", nil)
	if _, err = s.SetPermissions(ctx, "alice", private.ID, private.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); err != nil {
		t.Fatal("nested group did not see inherited draft", err)
	}
	page, err := s.Query(ctx, "bob", list.ID, QueryRequest{})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != item.ID {
		t.Fatalf("group query crossed exclusive ACL: %+v %v", page, err)
	}
	folder, err = s.SetPermissions(ctx, "alice", folder.ID, folder.Version, Permissions{CopyInherited: true})
	if err != nil {
		t.Fatal(err)
	}
	acl, err := s.Permissions(ctx, "alice", item.ID)
	if err != nil || len(acl.EffectiveGrants) != 1 || acl.EffectiveGrants[0].PrincipalType != "group" || acl.EffectiveGrants[0].PrincipalID != parent.ID {
		t.Fatalf("copy expanded group membership: %+v %v", acl, err)
	}
	// A user whose subject equals a group ID is never treated as that group.
	if err = d.EnsureUser(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, parent.ID, item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("principal namespaces collided", err)
	}
	if err = d.SetMember(ctx, "alice", child.ID, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("copied group did not revoke", err)
	}
	if err = d.SetMember(ctx, "alice", child.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	// Replacing the parent scope does not alter an exclusive copy below it.
	list, err = s.SetPermissions(ctx, "alice", list.ID, list.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); err != nil {
		t.Fatal("copy followed parent replacement", err)
	}
	folder, err = s.SetPermissions(ctx, "alice", folder.ID, folder.Version, Permissions{Inherit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("reset ignored new parent scope", err)
	}
	if _, err = s.Get(ctx, "bob", private.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("reset changed nested exclusive ACL", err)
	}
	if _, err = s.SetPermissions(ctx, "alice", folder.ID, folder.Version, Permissions{Grants: []Permission{{"group", "unknown", "read", "allow"}}}); err == nil {
		t.Fatal("unknown group accepted")
	}
	if _, err = s.SetPermissions(ctx, "alice", folder.ID, folder.Version, Permissions{Grants: []Permission{{"workspace_member", list.ID, "read", "allow"}}}); err == nil {
		t.Fatal("foreign workspace role accepted")
	}
}

func TestModuleRolesRestrictOwnersAndResolveGroupAssignments(t *testing.T) {
	s, w, list := fixture(t)
	ctx := testContext
	d := s.Directory()
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	item := create(t, s, list.ID, "item", "Owned", nil)
	if err := d.SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	role, err := d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Lists reader", Scopes: []string{"workspace.read", "list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); err != nil {
		t.Fatal("reader scope should retain owner read", err)
	}
	name := "Changed"
	if _, err = s.Update(ctx, "bob", item.ID, item.Version, UpdateResource{Name: &name}); !errors.Is(err, ErrForbidden) {
		t.Fatal("owner bypassed list.write", err)
	}
	if _, err = s.Create(ctx, "bob", list.ID, CreateResource{Kind: "item", Name: "Blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("parent ACL bypassed create scope", err)
	}
	if _, err = s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("workspace creation scope bypassed", err)
	}
	if _, err = s.CreateContentType(ctx, "bob", ContentTypeInput{Key: "blocked", Name: "Blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("owner bypassed contentType.manage", err)
	}
	if _, err = s.CreateTermGroup(ctx, "bob", TermGroupInput{Name: "Blocked"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("owner bypassed taxonomy.manage", err)
	}
	group, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Writers"})
	if err != nil {
		t.Fatal(err)
	}
	writeRole, err := d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "List contributor", Scopes: []string{"list.write"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", writeRole.ID, identity.Principal{Type: "group", ID: group.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", group.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Update(ctx, "bob", item.ID, item.Version, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", group.ID, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	// Workflow mutations use the same transactional service and persisted roles.
	if err = s.Write(ctx, func(tx *Service) error {
		_, e := tx.Update(ctx, "bob", item.ID, updated.Version, UpdateResource{Name: &item.Name})
		return e
	}); !errors.Is(err, ErrForbidden) {
		t.Fatal("transaction ignored role revocation", err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Browse(ctx, "bob", Browse{WorkspaceID: w.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("browse bypassed module scope", err)
	}
}

func TestWorkspaceManageRoleOverridesExclusiveScopes(t *testing.T) {
	s, w, list := fixture(t)
	ctx := context.Background()
	d := s.Directory()
	item := create(t, s, list.ID, "item", "Private", nil)
	if _, err := s.SetPermissions(ctx, "alice", item.ID, item.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	role, err := d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Workspace administrator", Scopes: []string{"workspace.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.Browse(ctx, "bob", Browse{ParentID: list.ID})
	if err != nil || len(page.Data) != 1 || page.Data[0].WorkspaceID != w.ID {
		t.Fatalf("administrator query: %+v %v", page, err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("administrator revocation ignored", err)
	}
}
