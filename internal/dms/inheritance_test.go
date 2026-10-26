package dms

import (
	"encoding/json"
	"slices"
	"testing"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/model"
)

// Blank lists accept the one Item catalog type, each through a list type of
// its own.
func TestBlankListsShareTheItemType(t *testing.T) {
	s, w, l := fixture(t)
	other := create(t, s, w.ID, "list", "Other", nil)
	first, err := s.contentType(testContext, l.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.contentType(testContext, other.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || *first.ParentID != *second.ParentID || first.Key != rootTypeKey {
		t.Fatal("blank lists", first, second)
	}
	items, err := s.DAL.Catalog().CountContentTypes(testContext, catalog.ContentTypeFilter{Catalog: true})
	if err != nil || items != 1 {
		t.Fatal("catalog holds one Item type", items, err)
	}
	if _, err = s.CreateContentType(testContext, "alice", ContentTypeInput{Key: rootTypeKey, Name: "Another item"}); !isValidation(err) {
		t.Fatal("reserved key accepted", err)
	}
}

// A subtype inherits its parent's fields; a list customizes its own list type
// without touching the catalog or other lists, and catalog changes reach every
// descendant.
func TestContentTypeInheritanceAcrossLists(t *testing.T) {
	s, w, documents := fixture(t)
	title, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "title", Label: "Title", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	amount, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "amount", Label: "Amount", Type: "integer", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	document, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "record", Name: "Record", FieldIds: []string{title.ID}})
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "invoice", Name: "Invoice", ParentID: document.ID, FieldIds: []string{amount.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(invoice.Effective.FieldIds) != 2 || invoice.Effective.FieldIds[0] != title.ID {
		t.Fatal("subtype schema", invoice)
	}
	if _, err = s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "copy", Name: "Copy", ParentID: document.ID, FieldIds: []string{title.ID}}); !isValidation(err) {
		t.Fatal("inherited field owned again", err)
	}
	invoices := create(t, s, w.ID, "list", "Bills", nil)
	documentType := attachOK(t, s, documents.ID, document.ID, true)
	invoiceType := attachOK(t, s, invoices.ID, invoice.ID, true)

	// A column of one list stays in that list.
	note := fieldOK(t, s, invoices.ID, CreateField{Key: "note", Label: "Note", Type: "text"})
	if invoiceType, err = s.contentType(testContext, invoices.ID, invoiceType.ID); err != nil || !slices.Contains(invoiceType.FieldIds, note.ID) {
		t.Fatal("list column", invoiceType, err)
	}
	if shared := catalogOf(t, s, invoiceType); slices.Contains(shared.Effective.FieldIds, note.ID) {
		t.Fatal("list column reached the catalog", shared)
	}
	if fields, err := s.Fields(testContext, "alice", documents.ID); err != nil || slices.ContainsFunc(fields, func(d *catalog.FieldDefinition) bool { return d.ID == note.ID }) {
		t.Fatal("list column reached another list", fields, err)
	}

	// A catalog change reaches every subtype and list type.
	due, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "due", Label: "Due", Type: "date"})
	if err != nil {
		t.Fatal(err)
	}
	document = catalogOf(t, s, documentType)
	if _, err = s.UpdateContentType(testContext, "alice", document.ID, document.Version, ContentTypeInput{Name: document.Name, FieldIds: []string{title.ID, due.ID}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{documents.ID, invoices.ID} {
		fields, err := s.Fields(testContext, "alice", c)
		if err != nil || !slices.ContainsFunc(fields, func(d *catalog.FieldDefinition) bool { return d.ID == due.ID }) {
			t.Fatal("catalog field did not reach the list", c, fields, err)
		}
	}

	// A list hides inherited fields from forms but cannot hide its own or a
	// required one without a default.
	invoiceType, _ = s.contentType(testContext, invoices.ID, invoiceType.ID)
	if _, err = s.UpdateListType(testContext, "alice", invoices.ID, invoiceType.ID, invoiceType.Version, ListTypeInput{FieldIds: invoiceType.FieldIds, HiddenFieldIds: []string{note.ID}}); !isValidation(err) {
		t.Fatal("own field hidden", err)
	}
	hidden, err := s.UpdateListType(testContext, "alice", invoices.ID, invoiceType.ID, invoiceType.Version, ListTypeInput{FieldIds: invoiceType.FieldIds, HiddenFieldIds: []string{due.ID}})
	if err != nil || !slices.Equal(hidden.Effective.HiddenFieldIds, []string{due.ID}) || !slices.Contains(hidden.Effective.FieldIds, due.ID) {
		t.Fatal("hidden field", hidden, err)
	}
	required, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "code", Label: "Code", Type: "text", Required: true})
	if err != nil {
		t.Fatal(err)
	}
	invoice = catalogOf(t, s, invoiceType)
	if _, err = s.UpdateContentType(testContext, "alice", invoice.ID, invoice.Version, ContentTypeInput{Name: invoice.Name, FieldIds: []string{amount.ID, required.ID}}); err != nil {
		t.Fatal(err)
	}
	hidden, _ = s.contentType(testContext, invoices.ID, invoiceType.ID)
	if _, err = s.UpdateListType(testContext, "alice", invoices.ID, hidden.ID, hidden.Version, ListTypeInput{FieldIds: hidden.FieldIds, HiddenFieldIds: []string{due.ID, required.ID}}); !isValidation(err) {
		t.Fatal("required field hidden", err)
	}

	// Filtering by a parent type matches its subtypes.
	item, err := s.Create(testContext, "alice", invoices.ID, CreateResource{Kind: "item", Name: "Bill", ContentTypeID: invoice.ID, Values: map[string]any{"title": "Power", "amount": json.Number("5"), "code": "A"}})
	if err != nil || *item.ContentTypeID != invoiceType.ID {
		t.Fatal("item named by its catalog type", item, err)
	}
	q, err := s.Query(testContext, "alice", invoices.ID, QueryRequest{Query: QuerySpec{ContentTypeID: document.ID, Filter: &FilterExpr{Field: "title", Value: []byte(`"Power"`)}}})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != item.ID {
		t.Fatal("parent type query", q, err)
	}
	if _, err = s.Query(testContext, "alice", invoices.ID, QueryRequest{Query: QuerySpec{ContentTypeID: document.ID, Filter: &FilterExpr{Field: "amount", Value: []byte(`5`)}}}); !isValidation(err) {
		t.Fatal("parent type query used a subtype field", err)
	}
	ids, err := s.DAL.Content().CollectionIDs(testContext, content.Collections{Reader: principal(testContext, "alice"), WorkspaceID: w.ID, ContentTypes: []string{"record"}})
	if err != nil || !slices.Contains(ids, documents.ID) || !slices.Contains(ids, invoices.ID) {
		t.Fatal("smart folder type selector", ids, err)
	}
	// The search index holds catalog types; a list type stands for its own.
	family, err := s.ContentTypeFamily(testContext, document.ID)
	if err != nil || len(family) != 2 || !slices.Contains(family, document.ID) || !slices.Contains(family, invoice.ID) {
		t.Fatal("search type family", family, err)
	}
	if family, err = s.ContentTypeFamily(testContext, invoiceType.ID); err != nil || !slices.Equal(family, []string{invoice.ID}) {
		t.Fatal("list type family", family, err)
	}
}

// A detached list type keeps its customizations for when its catalog type is
// attached again.
func TestReattachRestoresListCustomizations(t *testing.T) {
	s, _, l := fixture(t)
	code, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "code", Label: "Code", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	typ := typeOK(t, s, l.ID, ContentTypeInput{Key: "invoice", Name: "Invoice"})
	rules := []model.ValidationRule{{Key: "coded", Field: "code", Op: "required_if", WhenField: "code", WhenValue: []byte(`"x"`), Message: "Code"}}
	if _, err = s.UpdateListType(testContext, "alice", l.ID, typ.ID, typ.Version, ListTypeInput{FieldIds: []string{code.ID}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DetachContentType(testContext, "alice", l.ID, typ.ID, latest(t, s, l.ID).Version); err != nil {
		t.Fatal(err)
	}
	again := attachOK(t, s, l.ID, *typ.ParentID, false)
	if again.ID != typ.ID || !slices.Equal(again.FieldIds, []string{code.ID}) || len(again.Rules) != 1 {
		t.Fatal("customizations lost", again)
	}
}

// A field a type hides from forms cannot become one forms must fill.
func TestHiddenFieldsStayOptional(t *testing.T) {
	s, _, l := fixture(t)
	note, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "note", Label: "Note", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	typ := typeOK(t, s, l.ID, ContentTypeInput{Key: "memo", Name: "Memo", FieldIds: []string{note.ID}})
	if _, err = s.UpdateListType(testContext, "alice", l.ID, typ.ID, typ.Version, ListTypeInput{HiddenFieldIds: []string{note.ID}}); err != nil {
		t.Fatal(err)
	}
	required := true
	if _, err = s.UpdateCatalogField(testContext, "alice", note.ID, note.Version, UpdateField{Required: &required}); !isValidation(err) {
		t.Fatal("hidden field made mandatory", err)
	}
	withDefault := model.FieldOptions{DefaultValue: []byte(`"none"`)}
	if _, err = s.UpdateCatalogField(testContext, "alice", note.ID, note.Version, UpdateField{Required: &required, Options: &withDefault}); err != nil {
		t.Fatal("a default keeps a hidden field fillable", err)
	}
	item, err := s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Memo", ContentTypeID: typ.ID})
	if err != nil || item.Values["note"] != "none" {
		t.Fatal("item with a hidden required field", item, err)
	}
	current, err := s.CatalogFields(testContext, "alice")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(current, func(d *catalog.FieldDefinition) bool { return d.ID == note.ID })
	noDefault := model.FieldOptions{}
	if _, err = s.UpdateCatalogField(testContext, "alice", note.ID, current[i].Version, UpdateField{Options: &noDefault}); !isValidation(err) {
		t.Fatal("default removed from a hidden required field", err)
	}
}
