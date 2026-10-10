package identity_test

import (
	"context"
	"errors"
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
	if err := identity.RequireScope(ctx, db.SQL, user, "identity.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("bootstrap promoted another user: %v", err)
	}
}
