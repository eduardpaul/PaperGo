package database_test

import (
	"context"
	"papergo/internal/dms"
	"papergo/internal/testutil"
	"strings"
	"testing"
)

func TestIntegrityAndReadPlans(t *testing.T) {
	db := testutil.Database(t)
	s := dms.NewService(db.Client)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Item", Tags: []string{"finance"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec("UPDATE resources SET container_id=NULL WHERE id=?", i.ID); err == nil {
		t.Fatal("database allowed ownership change")
	}
	pub, err := s.Publish(ctx, "alice", i.ID, i.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.Exec("UPDATE publications SET snapshot='{}' WHERE id=?", pub.ID); err == nil {
		t.Fatal("database allowed snapshot mutation")
	}
	if _, err = db.SQL.Exec("DELETE FROM audit_events WHERE resource_id=?", i.ID); err == nil {
		t.Fatal("database allowed audit deletion")
	}
	page, err := s.Browse(ctx, "alice", dms.Browse{WorkspaceID: w.ID, Tag: "finance"})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("tag index: %+v %v", page, err)
	}
	tags := []string{"legal"}
	if _, err = s.Update(ctx, "alice", i.ID, i.Version+1, dms.UpdateResource{Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	page, err = s.Browse(ctx, "alice", dms.Browse{WorkspaceID: w.ID, Tag: "finance"})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("old tag still indexed")
	}
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{"SELECT id FROM resources WHERE workspace_id=? AND id>? ORDER BY id LIMIT 100", "resource_workspace_id_id", []any{w.ID, ""}},
		{"SELECT id FROM relationships WHERE source_id=? AND id>? ORDER BY id LIMIT 100", "relationship_source_id_id", []any{i.ID, ""}},
		{"SELECT id FROM relationships WHERE target_id=? AND id>? ORDER BY id LIMIT 100", "relationship_target_id_id", []any{i.ID, ""}},
		{"SELECT resource_id FROM resource_tags WHERE tag=?", "PRIMARY KEY", []any{"legal"}},
	} {
		rows, err := db.SQL.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		details := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details += detail
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(details, tc.index) || strings.Contains(details, "TEMP B-TREE") {
			t.Fatalf("query misses read index %s: %s", tc.index, details)
		}
	}
}
