package database_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dms"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIntegrityAndReadPlans(t *testing.T) {
	db := daltest.DatabaseAt(t, filepath.Join(t.TempDir(), "papergo.db"))
	s := dms.NewService(db.SQL)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Item", Keywords: []string{"finance"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.DAL.Resources().Change(ctx, i.ID, i.Version, "alice", resources.Changes{ParentID: &w.ID}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("DAL allowed invalid containment: %v", err)
	}
	pub, err := s.Publish(ctx, "alice", i.ID, i.Version)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := db.DAL.Revisions().Publications(ctx, revisions.PublicationFilter{ID: pub.ID})
	if err != nil || len(frozen) != 1 {
		t.Fatal(frozen, err)
	}
	page, err := s.Browse(ctx, "alice", dms.Browse{WorkspaceID: w.ID, Keyword: "finance"})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("keyword index: %+v %v", page, err)
	}
	keywords := []string{"legal"}
	if _, err = s.Update(ctx, "alice", i.ID, i.Version+1, dms.UpdateResource{Keywords: &keywords}); err != nil {
		t.Fatal(err)
	}
	retained, err := db.DAL.Revisions().Publications(ctx, revisions.PublicationFilter{ID: pub.ID})
	if err != nil || len(retained) != 1 || !reflect.DeepEqual(frozen[0].Snapshot, retained[0].Snapshot) {
		t.Fatalf("content edit changed the published snapshot: %+v %v", retained, err)
	}
	page, err = s.Browse(ctx, "alice", dms.Browse{WorkspaceID: w.ID, Keyword: "finance"})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("old keyword still indexed")
	}
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{"SELECT id FROM resources WHERE workspace_id=? AND kind='item' AND id>? ORDER BY id LIMIT 100", "resource_workspace_id_kind_id", []any{w.ID, ""}},
		{"SELECT id FROM relationships WHERE source_id=? AND type_id IS NULL AND id>? ORDER BY id LIMIT 100", "relationship_source_id_type_id_id", []any{i.ID, ""}},
		{"SELECT id FROM relationships WHERE target_id=? AND type_id IS NULL AND id>? ORDER BY id LIMIT 100", "relationship_target_id_type_id_id", []any{i.ID, ""}},
		{"SELECT resource_id FROM resource_keywords WHERE keyword=?", "PRIMARY KEY", []any{"legal"}},
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
