package dms

import (
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/businesskey"
	"papergo/ent/fieldvalue"
	"papergo/internal/model"
	"testing"
)

func isValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

func typeOK(t *testing.T, s *Service, collection string, in ContentTypeInput) *ent.ContentType {
	t.Helper()
	typ, err := s.CreateContentType(testContext, "alice", collection, latest(t, s, collection).Version, in)
	if err != nil {
		t.Fatal(err)
	}
	return typ
}

func fieldOK(t *testing.T, s *Service, collection string, in CreateField) *ent.FieldDefinition {
	t.Helper()
	d, err := s.CreateField(testContext, "alice", collection, in)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBulkContentTypesAndFrozenRules(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "title", Label: "Title", Type: "text", Required: true})
	typ := typeOK(t, s, l.ID, ContentTypeInput{Key: "invoice", Name: "Invoice", IsDefault: true, FieldKeys: []string{"title"}})
	for _, key := range []string{"subtotal", "total"} {
		fieldOK(t, s, l.ID, CreateField{ContentTypeID: typ.ID, Key: key, Label: key, Type: "decimal", Scale: 2, Indexed: true, Required: true})
	}
	typ, err := s.GetContentType(testContext, "alice", typ.ID)
	if err != nil {
		t.Fatal(err)
	}
	rules := []model.ValidationRule{{Key: "sum", Field: "total", Op: "gte", OtherField: "subtotal", Message: "Total must cover subtotal"}}
	typ, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, FieldKeys: typ.FieldKeys, IsDefault: true, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	good := &BulkCreate{Name: "Invoice", Values: map[string]any{"title": "One", "subtotal": "90071992547409.92", "total": "90071992547409.93"}}
	bad := &BulkCreate{Name: "Bad", Values: map[string]any{"title": "Bad", "subtotal": "90071992547409.94", "total": "90071992547409.93"}}
	bulkFails(t, s, "alice", l.ID, 1, nil, BulkOperation{Action: "create", Create: good}, BulkOperation{Action: "create", Create: bad})
	query, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ.ID}})
	if err != nil || query.Total != 0 {
		t.Fatal("rule failure did not roll back", query, err)
	}
	results := bulkOK(t, s, "alice", l.ID, BulkOperation{Action: "create", Create: good})
	item := latest(t, s, results[0].ID)
	if item.ContentTypeID == nil || *item.ContentTypeID != typ.ID {
		t.Fatal("default type", item)
	}
	rev, err := s.Client.ItemRevision.Get(testContext, *item.HeadRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Client.SchemaRevision.Get(testContext, rev.SchemaRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	_, frozen, err := typedDefinitions(snapshot.Definition, typ.ID)
	if err != nil || len(frozen) != 1 {
		t.Fatal(frozen, err)
	}
	oldTypes, err := s.ContentTypes(testContext, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	var simple *ent.ContentType
	for _, v := range oldTypes {
		if v.Key == "item" {
			simple = v
		}
	}
	bulkFails(t, s, "alice", l.ID, 0, nil, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Simple", ContentTypeID: simple.ID, Values: good.Values}})
	plain := bulkOK(t, s, "alice", l.ID, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Simple", ContentTypeID: simple.ID, Values: map[string]any{"title": "Plain"}}})
	query, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ.ID}})
	if err != nil || query.Total != 1 || query.Data[0].ID != item.ID {
		t.Fatal(query, err)
	}
	if _, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{ContentTypeID: simple.ID, Sort: SortSpec{Field: "total"}}}); !isValidation(err) {
		t.Fatal(err)
	}
	strict := []model.ValidationRule{{Key: "sum", Field: "total", Op: "lt", OtherField: "subtotal", Message: "Too high"}}
	version := latest(t, s, l.ID).Version
	if _, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, FieldKeys: typ.FieldKeys, IsDefault: true, Rules: strict}); !isValidation(err) {
		t.Fatal(err)
	}
	if latest(t, s, l.ID).Version != version {
		t.Fatal("invalid schema changed collection")
	}
	if latest(t, s, plain[0].ID).ContentTypeID == nil {
		t.Fatal("explicit type missing")
	}
	// A second collection cannot use this collection's type.
	other := create(t, s, *l.ParentID, "list", "Other", nil)
	bulkFails(t, s, "alice", other.ID, 0, ErrNotFound, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Wrong", ContentTypeID: typ.ID, Values: good.Values}})
}

func TestUniqueKeysReserveBothSurfacesAndRollbackBulk(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "code", Label: "Code", Type: "integer", Indexed: true, Options: model.FieldOptions{Unique: true}})
	item := create(t, s, l.ID, "item", "First", map[string]any{"code": json.Number("9007199254740993")})
	_, err := s.Publish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	values := map[string]any{"code": json.Number("9007199254740994")}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"9007199254740993", "9007199254740994"} {
		bulkFails(t, s, "alice", l.ID, 1, ErrConflict, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Rollback", Values: map[string]any{"code": json.Number("7")}}}, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Duplicate", Values: map[string]any{"code": json.Number(code)}}})
	}
	count, err := s.Client.BusinessKey.Query().Where(businesskey.ContainerIDEQ(l.ID)).Count(testContext)
	if err != nil || count != 2 {
		t.Fatal("claims leaked", count, err)
	}
	item, err = s.Unpublish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	second := create(t, s, l.ID, "item", "Freed", map[string]any{"code": json.Number("9007199254740993")})
	if err = s.Delete(testContext, "alice", second.ID, second.Version); err != nil {
		t.Fatal(err)
	}
	create(t, s, l.ID, "item", "Reused", map[string]any{"code": json.Number("9007199254740993")})
	// Optional nulls do not reserve a key.
	create(t, s, l.ID, "item", "Null one", nil)
	create(t, s, l.ID, "item", "Null two", nil)
}

func TestEnablingUniqueRejectsExistingDuplicatesAtomically(t *testing.T) {
	s, _, l := fixture(t)
	d := fieldOK(t, s, l.ID, CreateField{Key: "code", Label: "Code", Type: "text", Indexed: true})
	create(t, s, l.ID, "item", "One", map[string]any{"code": "same"})
	create(t, s, l.ID, "item", "Two", map[string]any{"code": "same"})
	version := latest(t, s, l.ID).Version
	options := model.FieldOptions{Unique: true}
	if _, err := s.UpdateField(testContext, "alice", l.ID, d.ID, version, UpdateField{Options: &options}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	got, err := s.Client.FieldDefinition.Get(testContext, d.ID)
	if err != nil || got.Options.Unique || latest(t, s, l.ID).Version != version {
		t.Fatal("partial schema", got, err)
	}
	for _, in := range []CreateField{{Key: "num", Label: "Num", Type: "number", Indexed: true, Options: options}, {Key: "multi", Label: "Multi", Type: "text", Indexed: true, Options: model.FieldOptions{Unique: true, Multiple: true}}, {Key: "unindexed", Label: "Unindexed", Type: "text", Options: options}} {
		if _, err = s.CreateField(testContext, "alice", l.ID, in); !isValidation(err) {
			t.Fatal(in, err)
		}
	}
}

func TestRequiredIfAndRemovalKeepHistory(t *testing.T) {
	s, _, l := fixture(t)
	fieldOK(t, s, l.ID, CreateField{Key: "approved", Label: "Approved", Type: "boolean"})
	d := fieldOK(t, s, l.ID, CreateField{Key: "code", Label: "Code", Type: "text", Indexed: true, Options: model.FieldOptions{Unique: true}})
	types, err := s.ContentTypes(testContext, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	typ := types[0]
	rules := []model.ValidationRule{{Key: "approval_code", Field: "code", Op: "required_if", WhenField: "approved", WhenValue: json.RawMessage("true"), Message: "Approved items need a code"}}
	typ, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, IsDefault: true, FieldKeys: typ.FieldKeys, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	bulkFails(t, s, "alice", l.ID, 0, nil, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Missing", Values: map[string]any{"approved": true}}})
	item := create(t, s, l.ID, "item", "Valid", map[string]any{"approved": true, "code": "A"})
	revID := *item.HeadRevisionID
	if err = s.DeleteField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version); !isValidation(err) {
		t.Fatal("dependent rule", err)
	}
	typ, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, IsDefault: true, FieldKeys: typ.FieldKeys})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Codes", Columns: []string{"code"}, Layout: "table"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version); !isValidation(err) {
		t.Fatal("dependent view", err)
	}
	if err = s.DeleteView(testContext, "alice", view.ID, view.Version); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version); err != nil {
		t.Fatal(err)
	}
	name := "Edited"
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := item.Values["code"]; exists {
		t.Fatal("removed field carried", item.Values)
	}
	old, err := s.Client.ItemRevision.Get(testContext, revID)
	if err != nil {
		t.Fatal(err)
	}
	oldValues, err := decodeValues(old.Payload)
	if err != nil || oldValues["code"] != "A" {
		t.Fatal("history rewritten", old, err)
	}
	oldSchema, err := s.Client.SchemaRevision.Get(testContext, old.SchemaRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	defs, _, err := typedDefinitions(oldSchema.Definition, typ.ID)
	if err != nil || definitionMap(defs)["code"] == nil {
		t.Fatal("old schema lost field", err)
	}
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "code", Label: "Reused", Type: "integer"}); !isValidation(err) {
		t.Fatal("reused removed key", err)
	}
	values := map[string]any{"code": "B"}
	bulkFails(t, s, "alice", l.ID, 0, nil, BulkOperation{Action: "update", ID: item.ID, Version: item.Version, Update: &BulkUpdate{Values: &values}})
	count, err := s.Client.FieldValue.Query().Where(fieldvalue.ContainerIDEQ(l.ID), fieldvalue.FieldKeyEQ("code")).Count(testContext)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	count, err = s.Client.BusinessKey.Query().Where(businesskey.ContainerIDEQ(l.ID)).Count(testContext)
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestTemplatesTargetContentTypeAndRules(t *testing.T) {
	s, w, l := fixture(t)
	typ := typeOK(t, s, l.ID, ContentTypeInput{Key: "event", Name: "Event"})
	tpl, err := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "event", Name: "Event", Fields: []CreateField{{Key: "start", Label: "Start", Type: "date", Required: true}, {Key: "end", Label: "End", Type: "date", Required: true}}, Rules: []model.ValidationRule{{Key: "dates", Field: "end", Op: "gte", OtherField: "start", Message: "End cannot precede start"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyTemplate(testContext, "alice", l.ID, tpl.ID, latest(t, s, l.ID).Version, tpl.Version, typ.ID); err != nil {
		t.Fatal(err)
	}
	// The default type still accepts an empty payload.
	create(t, s, l.ID, "item", "Plain", nil)
	bulkFails(t, s, "alice", l.ID, 0, nil, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Backwards", ContentTypeID: typ.ID, Values: map[string]any{"start": "2026-10-08", "end": "2026-10-07"}}})
	bulkOK(t, s, "alice", l.ID, BulkOperation{Action: "create", Create: &BulkCreate{Name: "Event", ContentTypeID: typ.ID, Values: map[string]any{"start": "2026-10-08", "end": "2026-10-09"}}})
}
