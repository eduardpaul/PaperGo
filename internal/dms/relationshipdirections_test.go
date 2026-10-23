package dms

import (
	"slices"
	"testing"
)

func TestRelationshipDirectionFilters(t *testing.T) {
	s, _, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	c := create(t, s, list.ID, "item", "C", nil)
	d := create(t, s, list.ID, "item", "D", nil)
	directed := true
	edges := map[string]string{}
	for _, in := range []struct {
		source, target, typ string
		directed            *bool
	}{{a.ID, b.ID, "out", &directed}, {c.ID, a.ID, "in", &directed}, {a.ID, d.ID, "related", nil}} {
		edge, err := s.Link(testContext, "alice", in.source, CreateRelationship{TargetID: in.target, Type: in.typ, Directed: in.directed})
		if err != nil {
			t.Fatal(err)
		}
		edges[in.typ] = edge.ID
	}
	all := []string{edges["out"], edges["in"], edges["related"]}
	for direction, want := range map[string][]string{"": all, "both": all, "incoming": {edges["in"]}, "outgoing": {edges["out"]}} {
		page, err := s.Relationships(testContext, "alice", a.ID, direction, "", "", 50)
		if err != nil {
			t.Fatal(direction, page, err)
		}
		got := make([]string, len(page.Data))
		for i, edge := range page.Data {
			got[i] = edge.ID
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("%s: edges %v, want %v", direction, got, want)
		}
	}
	for _, direction := range []string{"incoming", "outgoing"} {
		page, err := s.Relationships(testContext, "alice", d.ID, direction, "", "", 50)
		if err != nil || len(page.Data) != 0 {
			t.Fatal(direction, page, err)
		}
	}
}
