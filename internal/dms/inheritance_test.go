package dms

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"papergo/internal/dal/catalog"
	"papergo/internal/dal/content"
	"papergo/internal/dal/dberr"
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

	// A list's own field moves up when its catalog type gains it, and the
	// list type saves as before.
	invoice = catalogOf(t, s, invoiceType)
	if _, err = s.UpdateContentType(testContext, "alice", invoice.ID, invoice.Version, ContentTypeInput{Name: invoice.Name, FieldIds: append(slices.Clone(invoice.FieldIds), note.ID)}); err != nil {
		t.Fatal(err)
	}
	moved, err := s.contentType(testContext, invoices.ID, invoiceType.ID)
	if err != nil || slices.Contains(moved.FieldIds, note.ID) || !slices.Contains(moved.Effective.FieldIds, note.ID) {
		t.Fatal("field gained by the catalog type", moved, err)
	}
	if _, err = s.UpdateListType(testContext, "alice", invoices.ID, moved.ID, moved.Version, ListTypeInput{FieldIds: moved.FieldIds, HiddenFieldIds: moved.HiddenFieldIds}); err != nil {
		t.Fatal("list type after its field moved up", err)
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
	// Detaching may name the catalog type, as the other list type routes do.
	if _, err = s.DetachContentType(testContext, "alice", l.ID, *typ.ParentID, latest(t, s, l.ID).Version); err != nil {
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

// Removing a column from a list leaves the catalog and other lists as they
// are; the field goes once no type has it.
func TestRemovingAColumnStaysInTheList(t *testing.T) {
	s, w, l := fixture(t)
	other := create(t, s, w.ID, "list", "Other", nil)
	title, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "title", Label: "Title", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "code", Label: "Code", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "record", Name: "Record", FieldIds: []string{title.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{l.ID, other.ID} {
		typ := attachOK(t, s, c, record.ID, true)
		if _, err = s.UpdateListType(testContext, "alice", c, typ.ID, typ.Version, ListTypeInput{FieldIds: []string{code.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DeleteField(testContext, "alice", l.ID, title.ID, latest(t, s, l.ID).Version); !isValidation(err) {
		t.Fatal("inherited column removed", err)
	}
	if err = s.DeleteField(testContext, "alice", l.ID, code.ID, latest(t, s, l.ID).Version); err != nil {
		t.Fatal(err)
	}
	has := func(c, id string) bool {
		fields, err := s.Fields(testContext, "alice", c)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(fields, func(d *catalog.FieldDefinition) bool { return d.ID == id })
	}
	if has(l.ID, code.ID) || !has(other.ID, code.ID) || !has(l.ID, title.ID) {
		t.Fatal("column removal reached beyond the list")
	}
	if _, err = s.DAL.Catalog().Field(testContext, code.ID); err != nil {
		t.Fatal("field another list has was deleted", err)
	}
	if err = s.DeleteField(testContext, "alice", other.ID, code.ID, latest(t, s, other.ID).Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DAL.Catalog().Field(testContext, code.ID); !dberr.IsNotFound(err) {
		t.Fatal("unused field kept", err)
	}
}

// A list type stands for its catalog type in queries, so the list's items of
// subtypes match it too.
func TestListTypeQueriesIncludeSubtypes(t *testing.T) {
	s, _, l := fixture(t)
	title, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "title", Label: "Title", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "record", Name: "Record", FieldIds: []string{title.ID}})
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "invoice", Name: "Invoice", ParentID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	recordType := attachOK(t, s, l.ID, record.ID, true)
	invoiceType := attachOK(t, s, l.ID, invoice.ID, false)
	for _, typ := range []string{recordType.ID, invoiceType.ID} {
		if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: typ, ContentTypeID: typ, Values: map[string]any{"title": "Power"}}); err != nil {
			t.Fatal(err)
		}
	}
	for typ, want := range map[string]int{recordType.ID: 2, record.ID: 2, invoiceType.ID: 1} {
		page, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ, Filter: &FilterExpr{Field: "title", Value: []byte(`"Power"`)}}})
		if err != nil || len(page.Data) != want {
			t.Fatal("type query", typ, page, err)
		}
	}
}

// Only an unused catalog type can be deleted: no subtypes, no list types.
func TestDeleteUnusedContentTypes(t *testing.T) {
	s, _, l := fixture(t)
	record, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "record", Name: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "invoice", Name: "Invoice", ParentID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.GetContentType(testContext, "alice", *record.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteContentType(testContext, "alice", root.ID, root.Version); !isValidation(err) {
		t.Fatal("root deleted", err)
	}
	if err = s.DeleteContentType(testContext, "alice", record.ID, record.Version); !isValidation(err) {
		t.Fatal("type with a subtype deleted", err)
	}
	attachOK(t, s, l.ID, invoice.ID, false)
	if err = s.DeleteContentType(testContext, "alice", invoice.ID, invoice.Version); !isValidation(err) {
		t.Fatal("type a list uses deleted", err)
	}
	unused, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "unused", Name: "Unused", ParentID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteContentType(testContext, "alice", unused.ID, unused.Version+1); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale version", err)
	}
	if err = s.DeleteContentType(testContext, "alice", unused.ID, unused.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetContentType(testContext, "alice", unused.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted type still read", err)
	}
	// Its key is free again.
	if _, err = s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "unused", Name: "Again"}); err != nil {
		t.Fatal(err)
	}
}

// A catalog type's group is set and cleared with its name, and kept by
// schema changes.
func TestContentTypeGroups(t *testing.T) {
	s, _, _ := fixture(t)
	code, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "code", Label: "Code", Type: "text"})
	if err != nil {
		t.Fatal(err)
	}
	typ, err := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "invoice", Name: "Invoice", Group: "  Finance "})
	if err != nil || typ.Group != "Finance" {
		t.Fatal("group on creation", typ, err)
	}
	if _, err = s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "long", Name: "Long", Group: strings.Repeat("g", 101)}); !isValidation(err) {
		t.Fatal("long group", err)
	}
	if typ, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, Group: typ.Group, FieldIds: []string{code.ID}}); err != nil || typ.Group != "Finance" {
		t.Fatal("group kept on a schema edit", typ, err)
	}
	if err = s.DeleteCatalogField(testContext, "alice", code.ID, code.Version); err != nil {
		t.Fatal(err)
	}
	if typ, err = s.GetContentType(testContext, "alice", typ.ID); err != nil || typ.Group != "Finance" {
		t.Fatal("group kept when a field goes", typ, err)
	}
	if typ, err = s.UpdateContentType(testContext, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name}); err != nil || typ.Group != "" {
		t.Fatal("group cleared", typ, err)
	}
}
