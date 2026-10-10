package database_test

import (
	"context"
	"database/sql"
	"papergo/ent"
	"papergo/internal/dms"
	"papergo/internal/testutil"
	"testing"
)

// staleScopes lists resources whose trigger-maintained scope_id differs from the
// nearest exclusive ancestor-or-self found by walking the hierarchy.
func staleScopes(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`WITH RECURSIVE walk(id,at,inherit,depth) AS (
  SELECT id,id,inherit_permissions,0 FROM resources
  UNION ALL SELECT w.id,p.id,p.inherit_permissions,w.depth+1 FROM walk w JOIN resources r ON r.id=w.at JOIN resources p ON p.id=r.parent_id WHERE w.inherit AND w.depth<32
 ), nearest AS (SELECT id,at FROM walk WHERE NOT inherit)
 SELECT r.id FROM resources r LEFT JOIN nearest n ON n.id=r.id WHERE r.scope_id IS NOT n.at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stale := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		stale = append(stale, id)
	}
	return stale
}

func TestScopeFollowsInheritanceChanges(t *testing.T) {
	db := testutil.Database(t)
	s := dms.NewService(db.SQL)
	ctx := context.Background()
	create := func(parent, kind, name string) *ent.Resource {
		t.Helper()
		r, err := s.Create(ctx, "alice", parent, dms.CreateResource{Kind: kind, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	scope := func(id string) string {
		t.Helper()
		var v sql.NullString
		if err := db.SQL.QueryRow("SELECT scope_id FROM resources WHERE id=?", id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v.String
	}
	setACL := func(id string, in dms.Permissions) {
		t.Helper()
		r, err := s.Get(ctx, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetPermissions(ctx, "alice", id, r.Version, in); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string) {
		t.Helper()
		if stale := staleScopes(t, db.SQL); len(stale) > 0 {
			t.Fatalf("%s: stale scope_id on %v", step, stale)
		}
	}
	w := create("", "workspace", "W")
	l := create(w.ID, "list", "L")
	f := create(l.ID, "folder", "F")
	nested := create(f.ID, "folder", "Nested")
	deep := create(nested.ID, "item", "Deep")
	item := create(f.ID, "item", "Item")
	check("create")
	if scope(deep.ID) != w.ID {
		t.Fatal("new descendant did not inherit the workspace scope")
	}
	alice := dms.Permission{Subject: "alice", Action: "manage", Effect: "allow"}
	bob := dms.Permission{Subject: "bob", Action: "read", Effect: "allow"}
	setACL(nested.ID, dms.Permissions{Grants: []dms.Permission{alice}})
	setACL(l.ID, dms.Permissions{Grants: []dms.Permission{alice, bob}})
	check("break")
	if scope(item.ID) != l.ID || scope(deep.ID) != nested.ID {
		t.Fatalf("break: item %s deep %s", scope(item.ID), scope(deep.ID))
	}
	if _, err := s.Get(ctx, "bob", item.ID); err != nil {
		t.Fatal("grant on new scope not applied", err)
	}
	if _, err := s.Get(ctx, "bob", deep.ID); err == nil {
		t.Fatal("nested exclusive scope leaked the outer grant")
	}
	later := create(f.ID, "item", "Later")
	if scope(later.ID) != l.ID {
		t.Fatal("item created under a broken scope did not join it")
	}
	setACL(l.ID, dms.Permissions{Inherit: true})
	check("reset")
	if scope(later.ID) != w.ID || scope(deep.ID) != nested.ID {
		t.Fatalf("reset: later %s deep %s", scope(later.ID), scope(deep.ID))
	}
	if _, err := s.Get(ctx, "bob", item.ID); err == nil {
		t.Fatal("grant survived inheritance reset")
	}
}
