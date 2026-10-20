package identity_test

import (
	"context"
	"errors"
	"fmt"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/database"
	"papergo/internal/dal/directory"
	"papergo/internal/identity"
	"path/filepath"
	"testing"
)

func TestNestedGroupsRolesAndRevocation(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
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
	user := identity.Principal{Type: directory.PrincipalUser, ID: "identity-reader"}
	nested := identity.Principal{Type: directory.PrincipalGroup, ID: child.ID}
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
	if err = d.SetAssignment(ctx, admin, role.ID, identity.Principal{Type: directory.PrincipalGroup, ID: parent.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = identity.RequireScope(ctx, dal.NewStore(db.SQL), user.ID, "taxonomy.manage"); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, child.ID, identity.Principal{Type: directory.PrincipalGroup, ID: parent.ID}, false); err == nil {
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
	if err = identity.RequireScope(ctx, dal.NewStore(db.SQL), user.ID, "taxonomy.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("revocation: %v", err)
	}
	if _, err = d.SaveRole(ctx, admin, "", 0, identity.RoleInput{Name: "Unknown", Scopes: []string{"list.admin"}}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if err = d.SetAssignment(ctx, admin, identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: admin}, true); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatalf("last administrator: %v", err)
	}
}

func TestGroupAdministratorCannotManageUsersOrRoles(t *testing.T) {
	ctx := t.Context()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	role, err := d.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Group administrator", Scopes: []string{"group.read", "group.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	group, err := d.SaveGroup(ctx, "bob", "", 0, identity.GroupInput{Name: "Delegated group"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "bob", group.ID, identity.Principal{Type: directory.PrincipalUser, ID: "carol"}, false); err != nil {
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
			return d.SetAssignment(ctx, "bob", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false)
		},
	} {
		if err = action(); !errors.Is(err, identity.ErrForbidden) {
			t.Fatal("group administration granted user or role administration", err)
		}
	}
}

func TestEnrollmentAndBootstrapDoNotRestoreRemovedRoles(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	user := "limited-user"
	if err := d.EnsureUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: user}, true); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, dal.NewStore(db.SQL), user, "list.read"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("enrollment restored removed role: %v", err)
	}
	if err := d.Initialize(ctx, user, ""); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, dal.NewStore(db.SQL), user, "role.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("bootstrap promoted another user: %v", err)
	}
}

func TestNestedAdministratorProtectionAndGroupDepth(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	actor := "alice"
	var parent, leaf identity.Group
	for i := 0; i < directory.MaxGroupDepth; i++ {
		g, err := d.SaveGroup(ctx, actor, "", 0, identity.GroupInput{Name: fmt.Sprintf("Level %d", i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			parent = g
		} else if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: directory.PrincipalGroup, ID: g.ID}, false); err != nil {
			t.Fatal(err)
		}
		leaf = g
	}
	extra, err := d.SaveGroup(ctx, actor, "", 0, identity.GroupInput{Name: "Too deep"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: directory.PrincipalGroup, ID: extra.ID}, false); err == nil {
		t.Fatal("excessive group depth accepted")
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, actor, identity.AdministratorRole, identity.Principal{Type: directory.PrincipalGroup, ID: parent.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, actor, identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "organization-admin"}, true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, actor, leaf.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal("last nested administrator removed", err)
	}
	if err = d.DeleteGroup(ctx, actor, parent.ID, parent.Version); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal("last administrator group deleted", err)
	}
	if err = identity.RequireScope(ctx, dal.NewStore(db.SQL), "bob", "role.manage"); err != nil {
		t.Fatal("failed change did not roll back", err)
	}
}

func TestOIDCGroupSyncAndLocalIsolation(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	admin := "organization-admin"

	// Create local group Accounting and add alice to it
	accounting, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Accounting"})
	if err != nil {
		t.Fatal(err)
	}
	if accounting.Source != "local" {
		t.Fatalf("expected local source, got %s", accounting.Source)
	}
	if err = d.SetMember(ctx, admin, accounting.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}

	// 1. First sync with groups devs and qa
	err = d.SyncUser(ctx, identity.UserIdentity{
		Subject:           "alice",
		Name:              "Alice Smith",
		PreferredUsername: "asmith",
		Email:             "alice@example.com",
		Groups:            []string{"devs", "qa"},
		SyncGroups:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check user profile
	users, err := d.Users(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var aliceUser *identity.UserProfile
	for i := range users {
		if users[i].Subject == "alice" {
			aliceUser = &users[i]
			break
		}
	}
	if aliceUser == nil || aliceUser.DisplayName != "Alice Smith" || aliceUser.Email != "alice@example.com" {
		t.Fatalf("unexpected alice profile: %+v", aliceUser)
	}

	// Check groups created with source='oidc'
	groups, err := d.Groups(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	groupSources := map[string]directory.GroupSource{}
	for _, g := range groups {
		groupSources[g.Name] = g.Source
	}
	if groupSources["devs"] != directory.SourceOIDC || groupSources["qa"] != directory.SourceOIDC || groupSources["Accounting"] != directory.SourceLocal {
		t.Fatalf("unexpected group sources: %+v", groupSources)
	}

	// 2. Second sync removes qa, adds ops
	err = d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "alice",
		Groups:     []string{"devs", "ops"},
		SyncGroups: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify alice is member of devs, ops, and Accounting, but NOT qa
	groups, _ = d.Groups(ctx, admin)
	var devsID, opsID, qaID string
	for _, g := range groups {
		switch g.Name {
		case "devs":
			devsID = g.ID
		case "ops":
			opsID = g.ID
		case "qa":
			qaID = g.ID
		}
	}

	checkMember := func(groupID string, expected bool) {
		members, err := d.Members(ctx, admin, groupID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range members {
			if m.Type == "user" && m.ID == "alice" {
				found = true
				break
			}
		}
		if found != expected {
			t.Fatalf("group %s membership for alice: expected %v, got %v", groupID, expected, found)
		}
	}

	checkMember(devsID, true)
	checkMember(opsID, true)
	checkMember(accounting.ID, true) // Local group untouched!
	checkMember(qaID, false)         // Removed from qa!

	// 3. Sync token claiming local group name 'Accounting' should skip it
	err = d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "alice",
		Groups:     []string{"Accounting", "devs"},
		SyncGroups: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	groups, _ = d.Groups(ctx, admin)
	for _, g := range groups {
		if g.Name == "Accounting" && g.Source != "local" {
			t.Fatalf("Accounting group was modified to source %s", g.Source)
		}
	}
}

func TestOIDCGroupReadOnlyMemberAPIAndNesting(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	admin := "organization-admin"

	// Sync user to create an OIDC group
	if err := d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "bob",
		Groups:     []string{"engineers"},
		SyncGroups: true,
	}); err != nil {
		t.Fatal(err)
	}

	groups, err := d.Groups(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	var engineersGroup identity.Group
	for _, g := range groups {
		if g.Name == "engineers" {
			engineersGroup = g
			break
		}
	}
	if engineersGroup.ID == "" || engineersGroup.Source != "oidc" {
		t.Fatalf("expected oidc engineers group, got %+v", engineersGroup)
	}

	// Direct member addition to OIDC group must fail with ErrGroupManagedByIdentityProvider
	err = d.SetMember(ctx, admin, engineersGroup.ID, identity.Principal{Type: directory.PrincipalUser, ID: "charlie"}, false)
	if !errors.Is(err, identity.ErrGroupManagedByIdentityProvider) {
		t.Fatalf("expected ErrGroupManagedByIdentityProvider, got %v", err)
	}

	// Direct member deletion from OIDC group must fail
	err = d.SetMember(ctx, admin, engineersGroup.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true)
	if !errors.Is(err, identity.ErrGroupManagedByIdentityProvider) {
		t.Fatalf("expected ErrGroupManagedByIdentityProvider, got %v", err)
	}

	// Create a local group and nest the OIDC group into it
	localGrp, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "AllStaff"})
	if err != nil {
		t.Fatal(err)
	}
	err = d.SetMember(ctx, admin, localGrp.ID, identity.Principal{Type: directory.PrincipalGroup, ID: engineersGroup.ID}, false)
	if err != nil {
		t.Fatalf("failed to nest OIDC group in local group: %v", err)
	}

	// Assign role to the local group and verify bob inherits the scope
	role, err := d.SaveRole(ctx, admin, "", 0, identity.RoleInput{
		Name:   "StaffRole",
		Scopes: []string{"workspace.read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, admin, role.ID, identity.Principal{Type: directory.PrincipalGroup, ID: localGrp.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = identity.RequireScope(ctx, dal.NewStore(db.SQL), "bob", "workspace.read"); err != nil {
		t.Fatalf("bob failed to inherit scope through nested OIDC group: %v", err)
	}
}

func TestOIDCGroupSyncLastAdministratorProtection(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}

	// First sync alice to create oidc group cloud-admins
	if err := d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "alice",
		Groups:     []string{"cloud-admins"},
		SyncGroups: true,
	}); err != nil {
		t.Fatal(err)
	}

	groups, err := d.Groups(ctx, "organization-admin")
	if err != nil {
		t.Fatal(err)
	}
	var cloudAdminsID string
	for _, g := range groups {
		if g.Name == "cloud-admins" {
			cloudAdminsID = g.ID
			break
		}
	}
	if cloudAdminsID == "" {
		t.Fatal("cloud-admins group not found")
	}

	// Assign Administrator role to cloud-admins
	if err := d.SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalGroup, ID: cloudAdminsID}, false); err != nil {
		t.Fatal(err)
	}

	// Remove default organization-admin so alice is the sole administrator
	if err := d.SetAssignment(ctx, "alice", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "organization-admin"}, true); err != nil {
		t.Fatal(err)
	}

	// Alice tries to sync with empty groups (removed from cloud-admins at IdP)
	memberAudits := func() int {
		t.Helper()
		return daltest.Count(t, db.DAL, "audit_events", daltest.Row{"action": "group.member", "entity_id": cloudAdminsID})
	}
	beforeRemoval := memberAudits()
	if err := d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "alice",
		Groups:     []string{},
		SyncGroups: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Alice must STILL be in cloud-admins and have administrator scope (protected)
	if err := identity.RequireScope(ctx, dal.NewStore(db.SQL), "alice", "group.manage"); err != nil {
		t.Fatalf("last administrator was removed by group sync: %v", err)
	}
	if memberAudits() != beforeRemoval {
		t.Fatal("protected membership removal emitted a change audit")
	}

	// Once another administrator is assigned, identical claims must retry removal.
	if err := d.SetAssignment(ctx, "alice", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "carol"}, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncUser(ctx, identity.UserIdentity{Subject: "alice", Groups: []string{}, SyncGroups: true}); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, dal.NewStore(db.SQL), "alice", "workspace.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("removed IdP member retained administrator access: %v", err)
	}
	members, err := d.Members(ctx, "carol", cloudAdminsID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("removed IdP member retained group membership: %+v", members)
	}
	if memberAudits() != beforeRemoval+1 {
		t.Fatal("effective membership removal was not audited exactly once")
	}
}

func TestAdminGroupBootstrap(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.DAL.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	d := identity.Directory{DB: db.SQL}
	if err := d.Initialize(ctx, "", "platform-admins"); err != nil {
		t.Fatal(err)
	}

	// Verify platform-admins group exists as oidc group
	group, err := db.DAL.Directory().GroupByName(ctx, "platform-admins")
	if err != nil || group.Source != directory.SourceOIDC {
		t.Fatalf("expected platform-admins oidc group: %v, source: %s", err, group.Source)
	}
	groupID := group.ID

	// Verify Administrator role is assigned to platform-admins group
	if n := daltest.Count(t, db.DAL, "identity_assignments", daltest.Row{"role_id": "administrator", "principal_type": "group", "principal_id": groupID}); n != 1 {
		t.Fatalf("expected assignment to platform-admins group, count: %d", n)
	}

	// User superuser signs in with platform-admins group
	if err := d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "superuser",
		Groups:     []string{"platform-admins"},
		SyncGroups: true,
	}); err != nil {
		t.Fatal(err)
	}

	// superuser now has group.manage scope!
	if err := identity.RequireScope(ctx, dal.NewStore(db.SQL), "superuser", "group.manage"); err != nil {
		t.Fatalf("superuser failed to inherit group.manage: %v", err)
	}
}

func TestOIDCGroupSyncHashShortCircuit(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}

	u := identity.UserIdentity{
		Subject:           "dave",
		Name:              "Dave",
		PreferredUsername: "dave",
		Email:             "dave@example.com",
		Groups:            []string{"team-a"},
		SyncGroups:        true,
	}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	// Syncing with exact same data should succeed immediately
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	// Syncing with updated email should update the user
	u.Email = "dave.new@example.com"
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	users, _ := d.Users(ctx, "organization-admin")
	for _, user := range users {
		if user.Subject == "dave" && user.Email != "dave.new@example.com" {
			t.Fatalf("email was not updated: %+v", user)
		}
	}
}

func TestOIDCGroupSyncLimitsAndValidation(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}

	// 110 group names plus invalid empty string and too long name
	tooLong := ""
	for i := 0; i < 250; i++ {
		tooLong += "a"
	}
	groups := []string{"", tooLong}
	for i := 0; i < 110; i++ {
		groups = append(groups, fmt.Sprintf("grp-%03d", i))
	}

	err := d.SyncUser(ctx, identity.UserIdentity{
		Subject:    "eve",
		Groups:     groups,
		SyncGroups: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	allGroups, err := d.Groups(ctx, "organization-admin")
	if err != nil {
		t.Fatal(err)
	}
	oidcGroupCount := 0
	for _, g := range allGroups {
		if g.Source == "oidc" {
			oidcGroupCount++
		}
	}
	// Max allowed is 100
	if oidcGroupCount > 100 {
		t.Fatalf("expected at most 100 oidc groups, got %d", oidcGroupCount)
	}
}
