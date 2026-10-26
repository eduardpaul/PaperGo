package dms

import (
	"encoding/json"
	"papergo/internal/model"
	"testing"
)

func TestFolderBulkRulesAndBusinessKeys(t *testing.T) {
	s, _, c := fixture(t)
	fieldOK(t, s, c.ID, CreateField{Key: "code", Label: "Code", Type: "text", Indexed: true, Options: model.FieldOptions{Unique: true}})
	for _, key := range []string{"start", "end"} {
		fieldOK(t, s, c.ID, CreateField{Key: key, Label: key, Type: "integer", Required: true})
	}
	types, err := s.CollectionContentTypes(testContext, "alice", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	typ := types[0]
	if _, err = s.UpdateListType(testContext, "alice", c.ID, typ.ID, typ.Version, ListTypeInput{FieldIds: typ.FieldIds, Rules: []model.ValidationRule{{Key: "order", Field: "end", Op: "gte", OtherField: "start", Message: "End must follow start"}, {Key: "required", Field: "end", Op: "required_if", WhenField: "start", WhenValue: json.RawMessage(`1`), Message: "End required"}}}); err != nil {
		t.Fatal(err)
	}
	f := create(t, s, c.ID, "folder", "Folder", map[string]any{"code": "unique", "start": json.Number("1")})
	if _, err = s.Create(testContext, "alice", c.ID, CreateResource{Kind: "folder", Name: "Duplicate", Values: map[string]any{"code": "unique"}}); err == nil {
		t.Fatal("folder duplicate business key accepted")
	}
	bad := map[string]any{"start": json.Number("5"), "end": json.Number("4")}
	if _, err = s.Update(testContext, "alice", f.ID, f.Version, UpdateResource{Values: &bad}); !isValidation(err) {
		t.Fatal("folder comparison rule skipped", err)
	}
	patch := map[string]any{"code": "changed"}
	bulkOK(t, s, "alice", c.ID, BulkOperation{Action: "update", ID: f.ID, Version: f.Version, Update: &BulkUpdate{Values: &patch}})
	op, err := s.StartBulkUpdate(testContext, "alice", c.ID, FilteredBulkUpdate{Query: QuerySpec{Filter: &FilterExpr{Field: "code", Value: json.RawMessage(`"changed"`)}}, Values: map[string]any{"code": "final"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.runOperations(testContext); err != nil {
		t.Fatal(err)
	}
	status := bulkStatus(t, s, op.ID)
	if status.Matched != 1 || status.Updated != 1 || status.Failed != 0 || latest(t, s, f.ID).Values["code"] != "final" {
		t.Fatal(status)
	}
}
