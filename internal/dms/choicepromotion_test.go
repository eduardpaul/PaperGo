package dms

import (
	"encoding/json"
	"reflect"
	"testing"

	"papergo/internal/model"
)

func TestChoiceWideningAdoptsHeadsAndPreservesFrozenHistory(t *testing.T) {
	s, _, list := fixture(t)
	field := fieldOK(t, s, list.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"open", "closed"}, Indexed: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"open"`)}})
	item := create(t, s, list.ID, "item", "Invoice", map[string]any{"status": "closed"})
	publishItem(t, s, item)
	deleted := create(t, s, list.ID, "item", "Deleted", map[string]any{"status": "open"})
	if err := s.Delete(testContext, "alice", deleted.ID, deleted.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	options := field.Options
	options.Multiple = true
	updated, err := s.UpdateField(testContext, "alice", list.ID, field.ID, latest(t, s, list.ID).Version, UpdateField{Options: &options})
	if err != nil || !updated.Options.Multiple || string(updated.Options.DefaultValue) != `["open"]` {
		t.Fatal(updated, err)
	}
	item = latest(t, s, item.ID)
	if !reflect.DeepEqual(item.Values["status"], []any{"closed"}) {
		t.Fatal("head not widened", item.Values)
	}
	old, err := s.Revision(testContext, "alice", item.ID, 1)
	if err != nil || string(old.Payload) != `{"status":"closed"}` {
		t.Fatal("history changed", old, err)
	}
	published, err := s.GetSurface(testContext, "alice", item.ID, "published")
	if err != nil || published.Values["status"] != "closed" {
		t.Fatal("publication changed", published, err)
	}
	for _, surface := range []string{"head", "published"} {
		rows, err := s.Query(testContext, "alice", list.ID, QueryRequest{Surface: surface, Query: QuerySpec{Filter: &FilterExpr{Field: "status", Op: "eq", Value: json.RawMessage(`"closed"`)}}})
		if err != nil || len(rows.Data) != 1 {
			t.Fatal("index lost the existing choice", surface, rows, err)
		}
	}
	if _, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: ptr(map[string]any{"status": "open"})}); err == nil {
		t.Fatal("current multi-choice accepted a scalar")
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: ptr(map[string]any{"status": []any{"open", "closed"}})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateField(testContext, "alice", list.ID, field.ID, latest(t, s, list.ID).Version, UpdateField{Options: &model.FieldOptions{}}); err == nil {
		t.Fatal("cardinality narrowing accepted")
	}
	item, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version)
	if err != nil || !reflect.DeepEqual(item.Values["status"], []any{"closed"}) {
		t.Fatal("old history could not be restored through the current schema", item, err)
	}
	fresh := create(t, s, list.ID, "item", "Default", nil)
	if !reflect.DeepEqual(fresh.Values["status"], []any{"open"}) {
		t.Fatal("default not widened", fresh.Values)
	}
	restored, err := s.RestoreDeleted(testContext, "alice", list.ID, deleted.ID, false)
	if err != nil || !reflect.DeepEqual(restored.Values["status"], []any{"open"}) {
		t.Fatal("deleted item did not adopt widened choices", restored, err)
	}
}

func TestChoiceWideningRollsBackFailedSnapshot(t *testing.T) {
	s, _, list := fixture(t)
	field := fieldOK(t, s, list.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"open"}, Indexed: true})
	item := create(t, s, list.ID, "item", "Invoice", map[string]any{"status": "open"})
	if _, err := s.Client.ExecContext(testContext, `CREATE TRIGGER reject_promoted_snapshot BEFORE INSERT ON item_revisions WHEN json_type(new.payload,'$.status')='array' BEGIN SELECT RAISE(ABORT,'cannot record promoted revision'); END`); err != nil {
		t.Fatal(err)
	}
	before := latest(t, s, list.ID)
	if _, err := s.UpdateField(testContext, "alice", list.ID, field.ID, before.Version, UpdateField{Options: &model.FieldOptions{Multiple: true}}); err == nil {
		t.Fatal("widening succeeded after a snapshot write failed")
	}
	current, err := s.Client.FieldDefinition.Get(testContext, field.ID)
	if err != nil || current.Options.Multiple || current.Version != field.Version {
		t.Fatal("failed widening committed the catalog", current, err)
	}
	if latest(t, s, item.ID).Values["status"] != "open" || latest(t, s, list.ID).Version != before.Version {
		t.Fatal("failed widening changed live values or schema")
	}
}

func TestChoiceWideningUpdatesEveryAttachedCollection(t *testing.T) {
	s, ws, first := fixture(t)
	field, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "category", Label: "Category", Type: "choice", Choices: []string{"a", "b"}, Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	typ := typeOK(t, s, first.ID, ContentTypeInput{Key: "shared_choices", Name: "Shared choices", FieldIds: []string{field.ID}}, true)
	second, err := s.Create(testContext, "alice", ws.ID, CreateResource{Kind: "list", Name: "Second", ContentTypeIDs: []string{typ.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a := create(t, s, first.ID, "item", "A", map[string]any{"category": "a"})
	b := create(t, s, second.ID, "item", "B", map[string]any{"category": "b"})
	if _, err = s.UpdateCatalogField(testContext, "alice", field.ID, field.Version, UpdateField{Options: &model.FieldOptions{Multiple: true}}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ id, value string }{{a.ID, "a"}, {b.ID, "b"}} {
		if !reflect.DeepEqual(latest(t, s, item.id).Values["category"], []any{item.value}) {
			t.Fatal("shared field left a scalar in a collection", item.id)
		}
	}
}
