package identity_test

import (
	"encoding/json"
	"papergo/internal/identity"
	"papergo/internal/requestmeta"
	"papergo/internal/testutil"
	"reflect"
	"testing"
)

func TestOIDCSyncAuditsEffectiveChanges(t *testing.T) {
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	ctx := requestmeta.WithTrace(t.Context(), "oidc-sync-trace")
	u := identity.UserIdentity{Subject: "oidc-audit-user", Name: "Audit User", Email: "audit@example.com", Groups: []string{"audit-developers"}, SyncGroups: true}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE subject=?", u.Subject).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
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
	rows, err := db.SQL.QueryContext(ctx, "SELECT action,entity_type,entity_id,properties,trace_id FROM audit_events WHERE subject=? ORDER BY created_at,id", u.Subject)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []string{"user.create", "group.create", "group.member", "user.update", "group.member"}
	var actions []string
	for rows.Next() {
		var action, typ, id, properties, trace string
		if err := rows.Scan(&action, &typ, &id, &properties, &trace); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		if trace != "oidc-sync-trace" {
			t.Fatalf("sync audit lost request trace: %q", trace)
		}
		var changed []string
		if err := json.Unmarshal([]byte(properties), &changed); err != nil {
			t.Fatal(err)
		}
		if action == "group.member" && (typ != "identity.group" || !reflect.DeepEqual(changed, []string{"members"})) {
			t.Fatalf("incorrect membership audit: %s %v", typ, changed)
		}
		if action == "user.update" && (typ != "identity.user" || id != u.Subject || !reflect.DeepEqual(changed, []string{"email"})) {
			t.Fatalf("incorrect profile audit: %s %s %v", typ, id, changed)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("sync audit actions: got %v, want %v", actions, want)
	}
	var accessChanges int
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM live_changes c JOIN audit_events a ON a.id=c.entity_id WHERE c.kind='access' AND a.subject=? AND a.action='group.member'", u.Subject).Scan(&accessChanges); err != nil {
		t.Fatal(err)
	}
	if accessChanges != 2 {
		t.Fatalf("membership audits did not notify live access changes: %d", accessChanges)
	}
}

func TestOIDCSyncRollsBackWhenAuditFails(t *testing.T) {
	db := testutil.Database(t)
	d := identity.Directory{DB: db.SQL}
	ctx := t.Context()
	u := identity.UserIdentity{Subject: "oidc-rollback-user", Name: "Audit User", Email: "audit@example.com", Groups: []string{"rollback-original"}, SyncGroups: true}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	var hashBefore string
	if err := db.SQL.QueryRowContext(ctx, "SELECT sync_hash FROM identity_users WHERE id=?", u.Subject).Scan(&hashBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, `CREATE TRIGGER reject_sync_audit BEFORE INSERT ON audit_events WHEN NEW.subject='oidc-rollback-user' AND NEW.action='group.member' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
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
	var hashAfter string
	if err := db.SQL.QueryRowContext(ctx, "SELECT sync_hash FROM identity_users WHERE id=?", u.Subject).Scan(&hashAfter); err != nil {
		t.Fatal(err)
	}
	if hashAfter != hashBefore {
		t.Fatal("failed audit committed the sync hash")
	}
	var groups, audits int
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM identity_groups WHERE name='rollback-new'").Scan(&groups); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM audit_events WHERE subject=?", u.Subject).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if groups != 0 || audits != 3 {
		t.Fatalf("failed sync committed groups or partial audits: groups=%d, audits=%d", groups, audits)
	}
}
