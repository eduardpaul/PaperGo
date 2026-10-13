package dms

import (
	"testing"
)

func TestSearchMatchesTheLastWordAsAPrefix(t *testing.T) {
	s, _, c := fixture(t)
	acme := create(t, s, c.ID, "item", "Invoice INV-0000123 Acme Travel", nil)
	create(t, s, c.ID, "item", "Invoice INV-0000456 Globex Legal", nil)
	for search, want := range map[string]int{
		"acm":             1,
		"Acme tra":        1,
		"invoice":         2,
		"INV-0000":        2,
		"INV-0000123 acm": 1,
		"cme":             0,
		"travel acme":     0,
		`"acme`:           1,
		"---":             0,
		"*":               0,
	} {
		q, err := s.Query(testContext, "alice", c.ID, QueryRequest{Query: QuerySpec{Search: search}})
		if err != nil || len(q.Data) != want {
			t.Fatal("query search", search, len(q.Data), err)
		}
		page, err := s.Browse(testContext, "alice", Browse{ParentID: c.ID, Search: search})
		if err != nil || len(page.Data) != want {
			t.Fatal("browse search", search, len(page.Data), err)
		}
		if want == 1 && q.Data[0].ID != acme.ID {
			t.Fatal("wrong match", search)
		}
	}
}
