package dms

import (
	"encoding/json"
	"errors"
	"papergo/internal/model"
	"testing"
)

func TestItemPatchRetainsOmittedFieldsAndRemovesNull(t *testing.T) {
	s, _, list := fixture(t)
	fieldOK(t, s, list.ID, CreateField{Key: "a", Label: "A", Type: "text", Options: model.FieldOptions{DefaultValue: json.RawMessage(`"default"`)}})
	fieldOK(t, s, list.ID, CreateField{Key: "b", Label: "B", Type: "text"})
	item := create(t, s, list.ID, "item", "Item", map[string]any{"a": "one", "b": "two"})
	changes := map[string]any{"a": "changed"}
	updated, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &changes})
	if err != nil || updated.Values["a"] != "changed" || updated.Values["b"] != "two" {
		t.Fatal(updated, err)
	}
	changes = map[string]any{"a": nil}
	updated, err = s.Update(testContext, "alice", item.ID, updated.Version, UpdateResource{Values: &changes})
	if err != nil || updated.Values["b"] != "two" {
		t.Fatal(updated, err)
	}
	if _, exists := updated.Values["a"]; exists {
		t.Fatal("null failed to remove a defaulted value", updated.Values)
	}
	if len(changes) != 1 || changes["a"] != nil {
		t.Fatal("patch input mutated", changes)
	}
}

func TestRelationshipPatchRetainsRequiredFieldsAndRollsBackInvalidRemoval(t *testing.T) {
	s, workspace, list := fixture(t)
	a := create(t, s, list.ID, "item", "A", nil)
	b := create(t, s, list.ID, "item", "B", nil)
	typ, err := s.CreateRelationshipType(testContext, "alice", workspace.ID, RelationshipTypeInput{Key: "related", Label: "Related", Attributes: []CreateField{
		{Key: "required", Label: "Required", Type: "text", Required: true},
		{Key: "optional", Label: "Optional", Type: "text"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	edge, err := s.Link(testContext, "alice", a.ID, CreateRelationship{TypeID: typ.ID, TargetID: b.ID, Metadata: map[string]any{"required": "keep", "optional": "remove"}})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, edge.Version, UpdateRelationship{Metadata: map[string]any{"optional": nil}})
	if err != nil || updated.Metadata["required"] != "keep" {
		t.Fatal(updated, err)
	}
	if _, ok := updated.Metadata["optional"]; ok {
		t.Fatal("null retained", updated.Metadata)
	}
	var validation *ValidationError
	if _, err = s.UpdateRelationship(testContext, "alice", a.ID, edge.ID, updated.Version, UpdateRelationship{Metadata: map[string]any{"required": nil}}); !errors.As(err, &validation) {
		t.Fatal("required removal accepted", err)
	}
	kept, err := s.Client.Relationship.Get(testContext, edge.ID)
	if err != nil || kept.Version != updated.Version || kept.Metadata["required"] != "keep" {
		t.Fatal("invalid patch committed", kept, err)
	}
}
