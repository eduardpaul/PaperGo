package identity_test

import (
	"context"
	"errors"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/identity"
)

// Writing apps is administration: administrators hold app.manage, members
// do not, and a limited access token cannot.
func TestAppManageIsForAdministrators(t *testing.T) {
	d := identity.Directory{DB: daltest.Database(t).SQL}
	ctx := context.Background()
	store := dal.NewStore(d.DB)
	if _, err := d.CreateUser(ctx, "organization-admin", identity.UserInput{Subject: "app-member"}); err != nil {
		t.Fatal(err)
	}
	if err := identity.RequireScope(ctx, store, "organization-admin", "app.manage"); err != nil {
		t.Fatalf("an administrator cannot write apps: %v", err)
	}
	if err := identity.RequireScope(ctx, store, "app-member", "app.manage"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("a member writes apps: %v", err)
	}
	for _, scope := range identity.LimitableScopes {
		if scope == "app.manage" {
			t.Fatal("a limited token can hold app.manage")
		}
	}
}
