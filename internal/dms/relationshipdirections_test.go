package dms

import "testing"

func TestRelationshipDirectionFilters(t *testing.T) {
	s, _, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	c := create(t, s, list.ID, "item", "C", nil)
	d := create(t, s, list.ID, "item", "D", nil)
	directed := true
	for _, in := range []struct {
		source, target, typ string
		directed            *bool
	}{{a.ID, b.ID, "out", &directed}, {c.ID, a.ID, "in", &directed}, {a.ID, d.ID, "related", nil}} {
		if _, err := s.Link(testContext, "alice", in.source, CreateRelationship{TargetID: in.target, Type: in.typ, Directed: in.directed}); err != nil {
			t.Fatal(err)
		}
	}
	for direction, want := range map[string]int{"": 3, "both": 3, "incoming": 1, "outgoing": 1} {
		page, err := s.Relationships(testContext, "alice", a.ID, direction, "", "", 50)
		if err != nil || len(page.Data) != want {
			t.Fatal(direction, page, err)
		}
	}
	for _, direction := range []string{"incoming", "outgoing"} {
		page, err := s.Relationships(testContext, "alice", d.ID, direction, "", "", 50)
		if err != nil || len(page.Data) != 0 {
			t.Fatal(direction, page, err)
		}
	}
}
