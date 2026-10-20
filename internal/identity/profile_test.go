package identity_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
	"testing"
)

func TestUserProfilesAndAccountAnonymization(t *testing.T) {
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	d := s.Directory()
	ctx := context.Background()
	admin := "organization-admin"
	u, err := d.CreateUser(ctx, admin, identity.UserInput{Subject: "profile-user", DisplayName: "  Ada  ", Email: "ada@example.com"})
	if err != nil || u.DisplayName != "Ada" || u.Email != "ada@example.com" {
		t.Fatal(u, err)
	}
	if _, err = d.CreateUser(ctx, admin, identity.UserInput{Subject: u.Subject}); err == nil {
		t.Fatal("duplicate user accepted")
	}
	me, err := d.Me(ctx, u.Subject)
	if err != nil || me.Email != u.Email || len(me.Scopes) == 0 {
		t.Fatal(me, err)
	}
	name := "Grace"
	me, err = d.UpdateMe(ctx, u.Subject, &name)
	if err != nil || me.DisplayName != name || me.Email != u.Email {
		t.Fatal(me, err)
	}
	invalidEmail := "Name <ada@example.com>"
	if _, err = d.UpdateUser(ctx, admin, u.Subject, identity.UserUpdate{Email: &invalidEmail}); err == nil {
		t.Fatal("invalid email accepted")
	}
	longName := strings.Repeat("😀", 201)
	if _, err = d.UpdateMe(ctx, u.Subject, &longName); err == nil {
		t.Fatal("display name limit ignored")
	}
	group, err := d.SaveGroup(ctx, admin, "", 0, identity.GroupInput{Name: "Profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, admin, group.ID, identity.Principal{Type: directory.PrincipalUser, ID: u.Subject}, false); err != nil {
		t.Fatal(err)
	}
	theme := "dark"
	if _, err = d.PatchPreferences(ctx, u.Subject, false, 0, map[string]*string{"theme": &theme}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.CreateAccessToken(ctx, u.Subject, identity.AccessTokenInput{Name: "Test", Scopes: []string{"workspace.read"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUser(ctx, u.Subject, u.Subject); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("unprivileged deletion", err)
	}
	if err = s.DeleteUser(ctx, admin, admin); !errors.Is(err, identity.ErrCannotDeleteSelf) {
		t.Fatal(err)
	}
	if err = s.DeleteUser(ctx, "alice", admin); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal("last administrator deleted", err)
	}
	if err = s.DeleteUser(ctx, admin, u.Subject); err != nil {
		t.Fatal(err)
	}
	deleted, err := d.User(ctx, admin, u.Subject)
	if err != nil || deleted.Active || deleted.DeletedAt == nil || deleted.DisplayName != "Deleted user" || deleted.Email != "" {
		t.Fatal(deleted, err)
	}
	if err = d.EnsureUser(ctx, u.Subject); err != nil {
		t.Fatal(err)
	}
	if err = identity.RequireActive(ctx, dal.NewStore(db.SQL), u.Subject); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("deleted account admitted", err)
	}
	if _, err = d.SetUserActive(ctx, admin, u.Subject, true); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal("deleted account reactivated", err)
	}
	for table, column := range map[string]string{"identity_memberships": "principal_id", "identity_assignments": "principal_id", "identity_preferences": "owner_id", "access_tokens": "subject"} {
		if count := daltest.Count(t, db.DAL, table, daltest.Row{column: u.Subject}); count != 0 {
			t.Fatal(table, count)
		}
	}
	if _, err = d.Me(ctx, u.Subject); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("deleted profile active", err)
	}
}

func TestMePropagatesGroupReadErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		breakRead func(testing.TB, *dal.Store, string, string)
	}{
		{"query", daltest.HideColumn},
		{"scan", daltest.NullColumn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := daltest.Database(t)
			d := identity.Directory{DB: db.SQL}
			ctx := t.Context()
			if err := d.SyncUser(ctx, identity.UserIdentity{Subject: "me-read-error", Groups: []string{"me-error-group"}, SyncGroups: true}); err != nil {
				t.Fatal(err)
			}
			// Inject a broken database read without changing the application schema.
			tc.breakRead(t, db.DAL, "identity_groups", "name")
			if _, err := d.Me(ctx, "me-read-error"); err == nil {
				t.Fatal("Me returned success for a failed group read")
			}
		})
	}
}
