package dms

import "testing"

func TestAncestorKeepsEverythingBelowAFolder(t *testing.T) {
	s, _, c := fixture(t)
	top := create(t, s, c.ID, "folder", "2024", nil)
	sub := create(t, s, top.ID, "folder", "Acme", nil)
	deep := create(t, s, sub.ID, "item", "Acme contract", nil)
	direct := create(t, s, top.ID, "item", "Acme invoice", nil)
	create(t, s, c.ID, "item", "Acme letter", nil)
	other := create(t, s, c.ID, "folder", "2025", nil)
	create(t, s, other.ID, "item", "Acme order", nil)
	indexSearch(t, s)
	found := func(spec QuerySpec) map[string]bool {
		t.Helper()
		q, err := s.Query(testContext, "alice", c.ID, QueryRequest{IncludeTotal: true, Query: spec})
		if err != nil {
			t.Fatal(spec, err)
		}
		ids := map[string]bool{}
		for _, r := range q.Data {
			ids[r.ID] = true
		}
		if counted(q.Total) != len(ids) {
			t.Fatal("total differs from rows", counted(q.Total), len(ids))
		}
		return ids
	}
	if got := found(QuerySpec{AncestorID: top.ID, Search: "acme"}); len(got) != 3 || !got[deep.ID] || !got[direct.ID] || !got[sub.ID] {
		t.Fatal("below 2024", got)
	}
	if got := found(QuerySpec{AncestorID: c.ID, Search: "acme"}); len(got) != 5 {
		t.Fatal("whole library", got)
	}
	if got := found(QuerySpec{AncestorID: sub.ID}); len(got) != 1 || !got[deep.ID] {
		t.Fatal("below Acme", got)
	}
	if _, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{AncestorID: top.ID, ParentID: top.ID}}); err == nil {
		t.Fatal("parent_id and ancestor_id together")
	}
	if _, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{AncestorID: deep.ID}}); err == nil {
		t.Fatal("an item is not an ancestor")
	}
}
