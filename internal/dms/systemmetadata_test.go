package dms

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemMetadataUsesSelectedRevisionAndIndexes(t *testing.T) {
	s, w, list := fixture(t)
	item := create(t, s, list.ID, "item", "Original", nil)
	if _, err := s.Publish(testContext, "alice", item.ID, 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{Subject: "alice", Action: "manage"}, {Subject: "editor", Action: "write"}, {Subject: "reader", Action: "read"}}})
	if err != nil {
		t.Fatal(err)
	}
	name := "Draft"
	head, err := s.Update(testContext, "editor", item.ID, 2, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"head", "published"} {
		out, err := s.Query(testContext, "alice", list.ID, QueryRequest{Surface: surface, Query: QuerySpec{Filter: &FilterExpr{Field: "$modified_by", Op: "eq", Value: json.RawMessage(`"editor"`)}, Sort: SortSpec{Field: "$modified_at"}}})
		want := 0
		if surface == "head" {
			want = 1
		}
		if err != nil || out.Total != want {
			t.Fatalf("%s: %+v %v", surface, out, err)
		}
	}
	visible, err := s.Get(testContext, "reader", item.ID)
	if err != nil || visible.UpdatedBy != "alice" || visible.CreatedBy != "alice" {
		t.Fatalf("reader actor leaked: %+v %v", visible, err)
	}
	for _, subject := range []string{"alice", "reader"} {
		raw, _ := json.Marshal(head.UpdatedAt)
		out, err := s.Query(testContext, subject, list.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "$modified_at", Op: "gte", Value: raw}}})
		want := 0
		if subject == "alice" {
			want = 1
		}
		if err != nil || out.Total != want {
			t.Fatalf("visible time: %+v %v", out, err)
		}
	}
	groups, err := s.QueryGroups(testContext, "alice", list.ID, QueryRequest{Query: QuerySpec{GroupBy: "$modified_by"}})
	if err != nil || len(groups.Data) != 1 || groups.Data[0].Value != "editor" {
		t.Fatalf("actor groups: %+v %v", groups, err)
	}
	_, err = s.CreateView(testContext, "alice", list.ID, ViewInput{Name: "Recently edited", Columns: []string{"$modified_by", "$modified_at"}, Query: QuerySpec{Sort: SortSpec{Field: "$modified_at", Direction: "desc"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Client.QueryContext(testContext, "EXPLAIN QUERY PLAN SELECT item_id FROM item_surfaces WHERE container_id=? AND surface=? AND modified_by=? ORDER BY item_id", list.ID, "head", "editor")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "itemsurface_container_id_surface_modified_by_item_id") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}
