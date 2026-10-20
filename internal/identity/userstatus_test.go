package identity_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/identity"
	"testing"
)

func TestInactiveUserScopesAndLastActiveAdministrator(t *testing.T) {
	d := identity.Directory{DB: daltest.Database(t).SQL}
	ctx := context.Background()
	admin := "organization-admin"
	if _, err := d.CreateUser(ctx, admin, identity.UserInput{Subject: "status-member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SetUserActive(ctx, admin, admin, false); !errors.Is(err, identity.ErrLastAdministrator) {
		t.Fatal(err)
	}
	if _, err := d.SetUserActive(ctx, "status-member", admin, false); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal(err)
	}
	if _, err := d.SetUserActive(ctx, admin, "status-member", false); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureUser(ctx, "status-member"); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, dal.NewStore(d.DB), "status-member", "list.read"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("inactive user authorized", err)
	}
	if scopes, err := identity.EffectiveScopes(ctx, dal.NewStore(d.DB), "status-member"); err != nil || len(scopes) != 0 {
		t.Fatal(scopes, err)
	}
	if _, err := d.SetUserActive(ctx, admin, "missing", true); !errors.Is(err, identity.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := d.SetUserActive(ctx, admin, "status-member", true); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, dal.NewStore(d.DB), "status-member", "list.read"); err != nil {
		t.Fatal(err)
	}
}
