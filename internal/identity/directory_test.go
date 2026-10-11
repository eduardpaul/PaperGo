package identity_test

import (
	"context"
	"errors"
	"fmt"
	"papergo/internal/identity"
	"papergo/internal/testutil"
	"testing"
)

func TestNestedGroupsRolesAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	admin := "organization-admin"
	if err := d.EnsureUser(ctx, "identity-reader"); err != nil {
		t.Fatal(err)
	}
	parent, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	user := identity.Principal{Type: "user", ID: "identity-reader"}
	nested := identity.Principal{Type: "group", ID: child.ID}
	if err = d.SetMember(ctx, admin, child.ID, user, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, parent.ID, nested, false); err != nil {
		t.Fatal(err)
	}
	role, err := d.SaveRole(ctx, admin, "", 0, identity.RoleInput{Name: "Taxonomy curator", Scopes: []string{"taxonomy.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, admin, role.ID, identity.Principal{Type: "group", ID: parent.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = identity.RequireScope(ctx, db.SQL, user.ID, "taxonomy.manage"); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, child.ID, identity.Principal{Type: "group", ID: parent.ID}, false); err == nil {
		t.Fatal("cycle accepted")
	}
	if err = d.SetMember(ctx, user.ID, parent.ID, user, false); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("self-escalation: %v", err)
	}
	if _, err = d.SaveRole(ctx, "", "", 0, identity.RoleInput{Name: "Anonymous escalation", Scopes: []string{"role.manage"}}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("empty actor bypassed authorization", err)
	}
	if err = d.SetMember(ctx, admin, child.ID, user, true); err != nil {
		t.Fatal(err)
	}
	if err = identity.RequireScope(ctx, db.SQL, user.ID, "taxonomy.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revocation: %v", err)
	}
	if _, err = d.SaveRole(ctx, admin, "", 0, identity.RoleInput{Name: "Unknown", Scopes: []string{"list.admin"}}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if err = d.SetAssignment(ctx, admin, identity.AdministratorRole, identity.Principal{Type: "user", ID: admin}, true); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatalf("last administrator: %v", err)
	}
}

func TestGroupAdministratorCannotManageUsersOrRoles(t *testing.T) {
	ctx := t.Context()
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	role, err := d.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Group administrator", Scopes: []string{"group.read", "group.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	group, err := d.SaveGroup(ctx, "bob", "", 0, identity.GroupInput{Name: "Delegated group"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "bob", group.ID, identity.Principal{Type: "user", ID: "carol"}, false); err != nil {
		t.Fatal(err)
	}
	for _, action := range []func() error{
		func() error { _, err := d.CreateUser(ctx, "bob", identity.UserInput{Subject: "new-user"}); return err },
		func() error { _, err := d.SetUserActive(ctx, "bob", "carol", false); return err },
		func() error {
			_, err := d.SaveRole(ctx, "bob", "", 0, identity.RoleInput{Name: "Escalation", Scopes: []string{"role.manage"}})
			return err
		},
		func() error {
			return d.SetAssignment(ctx, "bob", identity.AdministratorRole, identity.Principal{Type: "user", ID: "bob"}, false)
		},
	} {
		if err = action(); !errors.Is(err, identity.ErrForbidden) {
			t.Fatal("group administration granted user or role administration", err)
		}
	}
}

func TestEnrollmentAndBootstrapDoNotRestoreRemovedRoles(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	user := "limited-user"
	if err := d.EnsureUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: "user", ID: user}, true); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, db.SQL, user, "list.read"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("enrollment restored removed role: %v", err)
	}
	if err := d.Initialize(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, db.SQL, user, "role.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("bootstrap promoted another user: %v", err)
	}
}

func TestNestedAdministratorProtectionAndGroupDepth(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	actor := "alice"
	var parent, leaf identity.Group
	for i := 0; i < identity.MaxGroupDepth; i++ {
		g, err := d.SaveGroup(ctx, actor, "", 0, identity.GroupInput{Name: fmt.Sprintf("Level %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			parent = g
		} else if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: "group", ID: g.ID}, false); err != nil {
			t.Fatal(err)
		}
		leaf = g
	}
	extra, err := d.SaveGroup(ctx, actor, "", 0, identity.GroupInput{Name: "Too deep"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: "group", ID: extra.ID}, false); err == nil {
		t.Fatal("excessive group depth accepted")
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, actor, identity.AdministratorRole, identity.Principal{Type: "group", ID: parent.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, actor, identity.AdministratorRole, identity.Principal{Type: "user", ID: "organization-admin"}, true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: "user", ID: "bob"}, true); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal("last nested administrator removed", err)
	}
	if err = d.DeleteGroup(ctx, actor, parent.ID, parent.Version); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal("last administrator group deleted", err)
	}
	if err = identity.RequireScope(ctx, db.SQL, "bob", "role.manage"); err != nil {
		t.Fatal("failed change did not roll back", err)
	}
}
