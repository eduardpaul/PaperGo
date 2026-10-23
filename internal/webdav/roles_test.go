package webdav_test

import (
	"papergo/internal/dal/directory"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"testing"
)

func TestWebDAVResolvesGroupACLAndRevokedModuleScopes(t *testing.T) {
	e := setup(t)
	d := e.s.Directory()
	g, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Library editors"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", g.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	lib, err := e.s.Get(ctx, "alice", e.library)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.s.SetPermissions(ctx, "alice", lib.ID, lib.Version, dms.Permissions{Grants: []dms.Permission{{PrincipalType: "group", PrincipalID: g.ID, Action: "write"}}}); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("PUT", e.root+"/group.txt", "bob-token", "%PDF-Created by group"), 201)
	if err = d.SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	role, err := d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Library reader", Scopes: []string{"item.read", "document.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("GET", e.root+"/group.txt", "bob-token", ""), 200)
	e.expect(e.do("PUT", e.root+"/blocked.txt", "bob-token", "%PDF-Denied by scope"), 403)
	if err = d.SetMember(ctx, "alice", g.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	e.expect(e.do("GET", e.root+"/group.txt", "bob-token", ""), 403)
}
