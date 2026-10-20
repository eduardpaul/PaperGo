package identity_test

import (
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"reflect"
	"slices"
	"testing"
)

func TestOIDCSyncAuditsEffectiveChanges(t *testing.T) {
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	ctx := requestmeta.WithTrace(t.Context(), "oidc-sync-trace")
	u := identity.UserIdentity{Subject: "oidc-audit-user", Name: "Audit User", Email: "audit@example.com", Groups: []string{"audit-developers"}, SyncGroups: true}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		return daltest.Count(t, db.DAL, "audit_events", daltest.Row{"subject": u.Subject})
	}
	if n := count(); n != 3 {
		t.Fatalf("expected user creation, group creation and membership audits, got %d", n)
	}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	// Changing only the sync hash must not emit a domain audit event.
	u.SyncGroups = false
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 3 {
		t.Fatalf("unchanged identity emitted audits: %d", n)
	}
	u.SyncGroups = true
	u.Groups = []string{}
	u.Email = "updated@example.com"
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 5 {
		t.Fatalf("expected profile and membership removal audits, got %d total", n)
	}
	events, err := db.DAL.Audit().List(ctx, auditstore.Filter{Actor: u.Subject})
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(events)
	want := []string{"user.create", "group.create", "group.member", "user.update", "group.member"}
	var actions []string
	accessChanges := 0
	for _, e := range events {
		actions = append(actions, e.Action)
		if e.TraceID != "oidc-sync-trace" {
			t.Fatalf("sync audit lost request trace: %q", e.TraceID)
		}
		if e.Action == "group.member" && (e.EntityType != "identity.group" || !reflect.DeepEqual(e.Properties, []string{"members"})) {
			t.Fatalf("incorrect membership audit: %s %v", e.EntityType, e.Properties)
		}
		if e.Action == "user.update" && (e.EntityType != "identity.user" || e.EntityID != u.Subject || !reflect.DeepEqual(e.Properties, []string{"email"})) {
			t.Fatalf("incorrect profile audit: %s %s %v", e.EntityType, e.EntityID, e.Properties)
		}
		if e.Action == "group.member" {
			accessChanges += daltest.Count(t, db.DAL, "live_changes", daltest.Row{"kind": "access", "entity_id": e.ID})
		}
	}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("sync audit actions: got %v, want %v", actions, want)
	}
	if accessChanges != 2 {
		t.Fatalf("membership audits did not notify live access changes: %d", accessChanges)
	}
}

func TestOIDCSyncRollsBackWhenAuditFails(t *testing.T) {
	db := daltest.Database(t)
	d := identity.Directory{DB: db.SQL}
	ctx := t.Context()
	u := identity.UserIdentity{Subject: "oidc-rollback-user", Name: "Audit User", Email: "audit@example.com", Groups: []string{"rollback-original"}, SyncGroups: true}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	hashBefore := daltest.Values(t, db.DAL, "identity_users", "sync_hash", daltest.Row{"id": u.Subject})
	daltest.Reject(t, db.DAL, "audit_events", daltest.OnInsert, daltest.Row{"subject": "oidc-rollback-user", "action": "group.member"})
	u.Email = "changed@example.com"
	u.Groups = []string{"rollback-new"}
	if err := d.SyncUser(ctx, u); err == nil {
		t.Fatal("sync succeeded without its membership audit")
	}
	me, err := d.Me(ctx, u.Subject)
	if err != nil {
		t.Fatal(err)
	}
	if me.Email != "audit@example.com" || !reflect.DeepEqual(me.Groups, []string{"rollback-original"}) {
		t.Fatalf("failed audit committed identity changes: %+v", me)
	}
	if hashAfter := daltest.Values(t, db.DAL, "identity_users", "sync_hash", daltest.Row{"id": u.Subject}); !reflect.DeepEqual(hashAfter, hashBefore) {
		t.Fatal("failed audit committed the sync hash")
	}
	groups := daltest.Count(t, db.DAL, "identity_groups", daltest.Row{"name": "rollback-new"})
	audits := daltest.Count(t, db.DAL, "audit_events", daltest.Row{"subject": u.Subject})
	if groups != 0 || audits != 3 {
		t.Fatalf("failed sync committed groups or partial audits: groups=%d, audits=%d", groups, audits)
	}
}
