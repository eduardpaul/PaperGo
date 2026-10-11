package identity_test

import (
	"context"
	"errors"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/testutil"
	"strings"
	"testing"
)

func TestUserProfilesAndAccountAnonymization(t *testing.T) {
	db := testutil.Database(t)
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
	if err = d.SetMember(ctx, admin, group.ID, identity.Principal{Type: "user", ID: u.Subject}, false); err != nil {
		t.Fatal(err)
	}
	theme := "dark"
	if _, err = d.PatchPreferences(ctx, u.Subject, false, 0, map[string]*string{"theme": &theme}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.ExecContext(ctx, "INSERT INTO webdav_credentials(id,created_at,subject,label,secret_hash) VALUES('profile-credential',CURRENT_TIMESTAMP,?,'Test',?)", u.Subject, strings.Repeat("a", 64)); err != nil {
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
	if err = identity.RequireActive(ctx, db.SQL, u.Subject); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("deleted account admitted", err)
	}
	if _, err = d.SetUserActive(ctx, admin, u.Subject, true); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal("deleted account reactivated", err)
	}
	for _, query := range []string{"SELECT count(*) FROM identity_memberships WHERE principal_id=?", "SELECT count(*) FROM identity_assignments WHERE principal_id=?", "SELECT count(*) FROM identity_preferences WHERE owner_id=?", "SELECT count(*) FROM webdav_credentials WHERE subject=?"} {
		var count int
		if err = db.SQL.QueryRowContext(ctx, query, u.Subject).Scan(&count); err != nil || count != 0 {
			t.Fatal(query, count, err)
		}
	}
	if _, err = d.Me(ctx, u.Subject); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("deleted profile active", err)
	}
}

func TestMePropagatesGroupReadErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		breakRead []string
	}{
		{"query", []string{"ALTER TABLE identity_groups RENAME COLUMN name TO unavailable_name"}},
		{"scan", []string{"ALTER TABLE identity_groups RENAME TO stored_groups", "CREATE VIEW identity_groups AS SELECT id,NULL AS name FROM stored_groups"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testutil.Database(t)
			d := identity.Directory{DB: db.SQL}
			ctx := t.Context()
			if err := d.SyncUser(ctx, identity.UserIdentity{Subject: "me-read-error", Groups: []string{"me-error-group"}, SyncGroups: true}); err != nil {
				t.Fatal(err)
			}
			// Inject a broken database read without changing the application schema.
			for _, statement := range tc.breakRead {
				if _, err := db.SQL.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Me(ctx, "me-read-error"); err == nil {
				t.Fatal("Me returned success for a failed group read")
			}
		})
	}
}
