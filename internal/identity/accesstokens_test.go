package identity_test

import (
	"context"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	"papergo/internal/identity"
)

func TestAccessTokenAuditUsesDirectoryEntity(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	store := dal.NewStore(db.SQL)
	created, err := d.CreateAccessToken(ctx, "organization-admin", identity.AccessTokenInput{Name: "Audit credential", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeAccessToken(ctx, "organization-admin", created.Token.ID); err != nil {
		t.Fatal(err)
	}
	events, err := store.Audit().List(ctx, audit.Filter{EntityType: directory.EntityAccessToken.AuditType(), EntityID: created.Token.ID})
	if err != nil || len(events) != 2 {
		t.Fatalf("access-token audits: %+v %v", events, err)
	}
	for i, change := range []directory.Change{directory.ChangeRevoke, directory.ChangeCreate} {
		if events[i].Action != directory.EntityAccessToken.AuditAction(change) {
			t.Fatalf("audit action: %+v", events[i])
		}
	}
	state, err := store.Directory().AuditState(ctx, directory.EntityAccessToken, created.Token.ID)
	if err != nil || state == nil || state["revoked_at"] == nil {
		t.Fatalf("access-token state: %v %v", state, err)
	}
}
