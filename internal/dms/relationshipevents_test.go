package dms

import (
	"testing"

	"papergo/ent"
	"papergo/ent/domainevent"
)

func TestRelationshipChangesTouchAndEmitForBothEndpoints(t *testing.T) {
	s, _, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	typ, err := s.CreateRelationshipType(testContext, "alice", RelationshipTypeInput{Name: "Related", Attributes: []CreateField{{Key: "note", Label: "Note", Type: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(action string, version int) {
		t.Helper()
		for _, item := range []string{a.ID, b.ID} {
			current := latest(t, s, item)
			if current.Version != version || current.UpdatedBy != "alice" || !current.UpdatedAt.After(a.UpdatedAt) {
				t.Fatal("endpoint modification was not visible", current)
			}
			rows, e := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(EventItemUpdated), domainevent.ResourceIDEQ(item)).Order(ent.Asc(domainevent.FieldID)).All(testContext)
			if e != nil || len(rows) != version-1 || rows[len(rows)-1].Data["relationship_action"] != action {
				t.Fatal("missing endpoint events", rows, e)
			}
			if revisionCount(t, s, item) != 1 {
				t.Fatal("relationship mutation recorded metadata history")
			}
		}
	}
	edge, err := s.Link(testContext, "alice", a.ID, CreateRelationship{Type: typ.ID, TargetID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	check("created", 2)
	if _, err = s.Link(testContext, "alice", b.ID, CreateRelationship{Type: typ.ID, TargetID: a.ID}); err != nil {
		t.Fatal(err)
	}
	check("created", 2)
	edge, err = s.UpdateRelationship(testContext, "alice", b.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"note": "changed"}})
	if err != nil {
		t.Fatal(err)
	}
	check("updated", 3)
	if _, err = s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, 1, UpdateRelationship{Metadata: map[string]any{"note": "stale"}}); err != ErrPreconditionFailed {
		t.Fatal(err)
	}
	check("updated", 3)
	if err = s.UnlinkVersion(testContext, "alice", a.ID, edge.ID, edge.Version); err != nil {
		t.Fatal(err)
	}
	check("deleted", 4)
}
