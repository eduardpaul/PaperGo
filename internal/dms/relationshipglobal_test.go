package dms

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestUntypedCrossWorkspaceRelationships(t *testing.T) {
	s, w1, l1 := fixture(t)
	w2 := create(t, s, "", "workspace", "Other", nil)
	l2 := create(t, s, w2.ID, "list", "Other list", nil)
	a := create(t, s, l1.ID, "item", "A", nil)
	b := create(t, s, l2.ID, "item", "B", nil)
	edge, err := s.Link(testContext, "alice", a.ID, CreateRelationship{TargetID: b.ID, Metadata: map[string]any{"note": "Cross workspace", "amount": json.Number("120"), "remove": nil}})
	if err != nil || edge.TypeID != "" || edge.Directed || edge.SourceID > edge.TargetID || len(edge.Metadata) != 2 {
		t.Fatal(edge, err)
	}
	retry, err := s.Link(testContext, "alice", b.ID, CreateRelationship{TargetID: a.ID})
	if err != nil || retry.ID != edge.ID || retry.Metadata["note"] != "Cross workspace" {
		t.Fatal(retry, err)
	}
	for _, w := range []string{w1.ID, w2.ID} {
		page, err := s.WorkspaceRelationships(testContext, "alice", w, "", nil, "attributes/amount gt 100", "", 50)
		if err != nil || len(page.Data) != 1 || page.Data[0].ID != edge.ID {
			t.Fatal(page, err)
		}
	}
	edge, err = s.UpdateRelationship(testContext, "alice", b.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"note": nil, "flag": true}})
	if err != nil || len(edge.Metadata) != 2 || edge.Metadata["flag"] != true {
		t.Fatal(edge, err)
	}
	directed, err := s.Link(testContext, "alice", a.ID, CreateRelationship{TargetID: b.ID, Directed: ptr(true)})
	if err != nil || directed.ID == edge.ID || !directed.Directed {
		t.Fatal(directed, err)
	}
	reverse, err := s.Link(testContext, "alice", b.ID, CreateRelationship{TargetID: a.ID, Directed: ptr(true)})
	if err != nil || reverse.ID == directed.ID {
		t.Fatal(reverse, err)
	}
	if _, err = s.Link(testContext, "alice", a.ID, CreateRelationship{Type: "typed", TargetID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w1.ID, WorkspaceMember{"bob", "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(testContext, "bob", a.ID, CreateRelationship{TargetID: b.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("unwritable peer accepted", err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w2.ID, WorkspaceMember{"bob", "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Link(testContext, "bob", a.ID, CreateRelationship{TargetID: b.ID}); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{{"nested": map[string]any{"x": true}}, {"array": []any{true}}, {"bad/key": true}} {
		if _, err = s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: values}); err == nil {
			t.Fatal("invalid untyped attributes", values)
		}
	}
	// The partial unique index enforces untyped identity at the database boundary.
	if _, err = s.Client.Relationship.Create().SetSourceID(edge.SourceID).SetTargetID(edge.TargetID).SetDirected(false).Save(testContext); err == nil {
		t.Fatal("duplicate null type accepted")
	}
}
