package database_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dms"
	"papergo/internal/testutil"
	"testing"
	"time"
)

func TestApplicationUpgradePreservesIndexesHistoryAndAugmentedACLs(t *testing.T) {
	db := testutil.DatabaseThrough(t, "20261007000400")
	ctx := context.Background()
	now := time.Now().UTC()
	tx, e := db.SQL.Begin()
	if e != nil {
		t.Fatal(e)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, e := tx.Exec(query, args...); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	resource := func(id, kind string, parent, container any, inherit bool) {
		exec(`INSERT INTO resources(id,created_at,updated_at,workspace_id,kind,name,tags,"values",inherit_permissions,version,parent_id,container_id) VALUES(?,?,?,'w',?,?,'[]','{}',?,1,?,?)`, id, now, now, kind, id, inherit, parent, container)
	}
	resource("w", "workspace", nil, nil, false)
	resource("l", "list", "w", nil, true)
	resource("i", "item", "l", "l", true)
	exec("INSERT INTO field_definitions(id,created_at,key,label,type,required,choices,container_id,indexed,scale) VALUES('f',?,'serial','Serial','integer',0,'[]','l',1,0)", now)
	schema := `{"fields":[{"id":"serial","label":"Serial","type":"integer","required":false,"choices":[],"indexed":true,"scale":0}]}`
	exec("INSERT INTO schema_revisions(id,created_at,container_id,revision_number,definition,created_by) VALUES('s',?,'l',1,?,'alice')", now, schema)
	exec("UPDATE resources SET schema_head_id='s' WHERE id='l'")
	payload := `{"serial":9007199254740993}`
	exec("INSERT INTO item_revisions(id,created_at,item_id,container_id,schema_revision_id,revision_number,name,tags,payload,created_by) VALUES('v',?,'i','l','s',1,'Exact','[]',?,'alice')", now, payload)
	for _, surface := range []string{"head", "published"} {
		exec("INSERT INTO item_surfaces(id,created_at,item_id,container_id,workspace_id,surface,revision_id,name,tags,payload) VALUES(?,?,'i','l','w',?,'v','Exact','[]',?)", surface, now, surface, payload)
		exec("INSERT INTO field_values(id,created_at,surface_id,container_id,item_id,surface,field_key,field_type,scale,value_integer) VALUES(?, ?,?,'l','i',?,'serial','integer',0,9007199254740993)", "f-"+surface, now, surface, surface)
	}
	exec(`UPDATE resources SET head_revision_id='v',published_revision_id='v',next_revision_number=2,name='Exact',"values"=? WHERE id='i'`, payload)
	for _, g := range []struct{ subject, action, scope string }{{"alice", "manage", "w"}, {"reader", "read", "w"}, {"carol", "read", "l"}} {
		exec("INSERT INTO grants(id,created_at,subject,action,effect,resource_id) VALUES(?,?,?,?,'allow',?)", g.subject, now, g.subject, g.action, g.scope)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	files := testutil.MigrationFiles(t)
	tx, e = db.SQL.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	for _, f := range files {
		if f.Version() <= "20261007000400" {
			continue
		}
		if _, e = tx.Exec(string(f.Bytes())); e != nil {
			t.Fatal(f.Name(), e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if stale := staleScopes(t, db.SQL); len(stale) > 0 {
		t.Fatal("upgrade left scope_id unset or stale", stale)
	}
	s := dms.NewService(db.Client)
	for _, actor := range []string{"reader", "carol", "alice"} {
		got, e := s.Get(ctx, actor, "i")
		if e != nil || got.Values["serial"] != json.Number("9007199254740993") {
			t.Fatal("access/content lost", actor, got, e)
		}
	}
	acl, e := s.Permissions(ctx, "alice", "l")
	if e != nil || acl.Inherit || len(acl.Grants) != 3 {
		t.Fatal("legacy grant union not preserved", acl, e)
	}
	q, e := s.Query(ctx, "reader", "l", dms.QueryRequest{Query: dms.QuerySpec{Filter: &dms.FilterExpr{Field: "serial", Value: json.RawMessage("9007199254740993")}}})
	if e != nil || q.Total != 1 {
		t.Fatal("old scalar index lost", q, e)
	}
	var failures int
	rows, e := db.SQL.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		failures++
	}
	rows.Close()
	if failures != 0 {
		t.Fatal("foreign key failures", failures)
	}
	var integrity string
	if e = db.SQL.QueryRow("PRAGMA integrity_check").Scan(&integrity); e != nil || integrity != "ok" {
		t.Fatal(integrity, e)
	}
}
