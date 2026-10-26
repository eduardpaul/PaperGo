package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/jsonutil"
	"papergo/internal/model"
	"sync"
	"testing"
)

func TestContentTypeRepository_SQLite(t *testing.T) {
	testContentTypeRepository(t, daltest.SQLiteStore(t))
}
func TestContentTypeRepository_Postgres(t *testing.T) {
	testContentTypeRepository(t, daltest.PostgresStore(t))
}
func testContentTypeRepository(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Catalog()
	field, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "field", Key: "amount", Label: "Amount", Type: catalog.FieldInteger, Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	var rules []model.ValidationRule
	if err := json.Unmarshal([]byte(`[{"key":"floor","field":"amount","op":"required_if","when_field":"amount","when_value":9007199254740993}]`), &rules); err != nil {
		t.Fatal(err)
	}
	recipe := "recipe"
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "invoices", Key: "invoice", Name: "Invoices", TemplateKey: &recipe, FieldIds: []string{field.ID}, Rules: rules}); err != nil {
		t.Fatal(err)
	}
	typ, err := r.ContentType(ctx, "invoices")
	if err != nil {
		t.Fatal(err)
	}
	if len(typ.Rules) != 1 || !jsonutil.Equal(typ.Rules[0].WhenValue, []byte(`9007199254740993`)) {
		t.Fatalf("rule precision lost: %+v", typ.Rules)
	}
	listType, _, err := r.Attach(ctx, f.Source, typ.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{FieldID: field.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != typ.ID {
		t.Fatalf("JSON membership of own fields: %+v %v", rows, err)
	}
	found, err := s.Catalog().OneContentType(ctx, catalog.ContentTypeFilter{TemplateKey: "recipe"})
	if err != nil || found.ID != typ.ID {
		t.Fatalf("template lookup: %+v %v", found, err)
	}
	if selected, err := s.Catalog().CollectionContentType(ctx, f.Source, listType.ID); err != nil || selected.ID != listType.ID || *selected.ParentID != typ.ID {
		t.Fatalf("collection lookup: %+v %v", selected, err)
	}
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "bad", Key: "bad", Name: "Bad", FieldIds: []string{field.ID, field.ID}}); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("duplicate field identity accepted: %v", err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Go(func() {
			_, results[i] = r.SaveContentType(ctx, catalog.ContentType{ID: typ.ID, Name: "Updated", FieldIds: []string{field.ID}}, typ.Version)
		})
	}
	wg.Wait()
	won, lost := 0, 0
	for _, err := range results {
		if err == nil {
			won++
		} else if errors.Is(err, dberr.ErrConcurrentUpdate) {
			lost++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("content type update: %d winners, %d stale", won, lost)
	}
	current, err := s.Catalog().ContentType(ctx, typ.ID)
	if err != nil || current.Version != 2 {
		t.Fatalf("content type version: %+v %v", current, err)
	}
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: typ.ID, Name: "Missing field", FieldIds: []string{"missing"}}, 2); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	current, err = s.Catalog().ContentType(ctx, typ.ID)
	if err != nil || current.Version != 2 || current.Name != "Updated" {
		t.Fatalf("failed write committed: %+v %v", current, err)
	}
	for i := range results {
		wg.Go(func() { results[i] = s.Catalog().BumpContentTypesForField(ctx, field.ID) })
	}
	wg.Wait()
	for _, err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err = s.Catalog().ContentType(ctx, typ.ID)
	if err != nil || current.Version != 4 {
		t.Fatalf("concurrent catalog bump lost: %+v %v", current, err)
	}
	cleared, err := r.SaveContentType(ctx, catalog.ContentType{ID: typ.ID, Name: "Cleared"}, 4)
	if err != nil || len(cleared.FieldIds) != 0 {
		t.Fatalf("clear fields: %+v %v", cleared, err)
	}
	if listType, err = r.ContentType(ctx, listType.ID); err != nil || listType.Name != "Cleared" || listType.Key != "invoice" {
		t.Fatalf("list type follows its catalog type: %+v %v", listType, err)
	}

}

func TestCollectionAttachments_SQLite(t *testing.T) {
	testCollectionAttachments(t, daltest.SQLiteStore(t))
}
func TestCollectionAttachments_Postgres(t *testing.T) {
	testCollectionAttachments(t, daltest.PostgresStore(t))
}
func testCollectionAttachments(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Catalog()
	for _, key := range []string{"memo", "letter"} {
		if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: key, Key: key, Name: key}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "memo-copy", Key: "memo", Name: "Memo copy"}); !errors.Is(err, dberr.ErrUniqueViolation) {
		t.Fatalf("duplicate catalog key: %v", err)
	}
	defaults := func() string {
		t.Helper()
		typ, err := r.CollectionContentType(ctx, f.Destination, "")
		if err != nil {
			t.Fatal(err)
		}
		return *typ.ParentID
	}
	// The fixture attached "type" as the default; others join without it.
	memo, changed, err := r.Attach(ctx, f.Destination, "memo", false)
	if err != nil || !changed || defaults() != "type" || memo.Key != "memo" || memo.Catalog() {
		t.Fatalf("attach: %+v %v %v %s", memo, changed, err, defaults())
	}
	if again, changed, err := r.Attach(ctx, f.Destination, "memo", false); err != nil || changed || again.ID != memo.ID {
		t.Fatalf("reattach: %+v %v %v", again, changed, err)
	}
	if _, _, err := r.Attach(ctx, f.Destination, memo.ID, false); err == nil {
		t.Fatal("attached a list type")
	}
	if _, _, err := r.Attach(ctx, f.Folder, "memo", false); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("attached to a folder: %v", err)
	}
	letter, _, err := r.Attach(ctx, f.Destination, "letter", true)
	if err != nil || defaults() != "letter" {
		t.Fatalf("new default: %v %s", err, defaults())
	}
	if ok, err := r.Accepts(ctx, f.Destination, memo.ID); err != nil || !ok {
		t.Fatalf("accepts: %v %v", ok, err)
	}
	if ok, err := r.Accepts(ctx, f.Destination, "memo"); err != nil || ok {
		t.Fatalf("accepts a catalog type: %v %v", ok, err)
	}
	collections, err := r.AttachedCollections(ctx, []string{"letter", "memo"})
	if err != nil || len(collections) != 1 || collections[0] != f.Destination {
		t.Fatalf("attached collections: %v %v", collections, err)
	}
	if collections, err = r.AttachedCollections(ctx, []string{"type"}); err != nil || len(collections) != 2 {
		t.Fatalf("collections of a catalog type: %v %v", collections, err)
	}
	// Detaching the default promotes the remaining type with the lowest key.
	if err := r.Detach(ctx, f.Destination, letter.ID); err != nil || defaults() != "type" {
		t.Fatalf("detach default: %v %s", err, defaults())
	}
	if again, changed, err := r.Attach(ctx, f.Destination, "letter", false); err != nil || !changed || again.ID != letter.ID {
		t.Fatalf("attaching again restores the list type: %+v %v %v", again, changed, err)
	}
	if err := r.Detach(ctx, f.Destination, letter.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Attach(ctx, f.Source, "letter", false); err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(ctx, f.Source, f.SourceType); !errors.Is(err, catalog.ErrContentTypeInUse) {
		t.Fatalf("detached a type in use: %v", err)
	}
	if err := r.Detach(ctx, f.Destination, memo.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(ctx, f.Destination, f.DestinationType); !errors.Is(err, catalog.ErrLastContentType) {
		t.Fatalf("detached the last type: %v", err)
	}
	if err := r.Detach(ctx, f.Destination, memo.ID); !dberr.IsNotFound(err) {
		t.Fatalf("detached twice: %v", err)
	}
	if id, err := r.Counterpart(ctx, f.Destination, f.SourceType); err != nil || id != f.DestinationType {
		t.Fatalf("counterpart: %s %v", id, err)
	}
	if _, err := r.Counterpart(ctx, f.Destination, memo.ID); !dberr.IsNotFound(err) {
		t.Fatalf("counterpart of a detached type: %v", err)
	}
}

func TestContentTypeInheritance_SQLite(t *testing.T) {
	testContentTypeInheritance(t, daltest.SQLiteStore(t))
}
func TestContentTypeInheritance_Postgres(t *testing.T) {
	testContentTypeInheritance(t, daltest.PostgresStore(t))
}
func testContentTypeInheritance(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Catalog()
	fields := map[string]*catalog.FieldDefinition{}
	for _, key := range []string{"title", "amount", "due", "note"} {
		field, err := r.CreateField(ctx, catalog.FieldDefinition{ID: key, Key: key, Label: key, Type: catalog.FieldText})
		if err != nil {
			t.Fatal(err)
		}
		fields[key] = field
	}
	if _, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "amount-copy", Key: "amount", Label: "Amount", Type: catalog.FieldText}); err != nil {
		t.Fatal(err)
	}
	rule := model.ValidationRule{Key: "titled", Field: "title", Op: "required"}
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: "type", FieldIds: []string{"title"}, Rules: []model.ValidationRule{rule}}, 1); err != nil {
		t.Fatal(err)
	}
	parent := "type"
	invoice, err := r.CreateContentType(ctx, catalog.ContentType{ID: "invoice", Key: "invoice", Name: "Invoice", ParentID: &parent, FieldIds: []string{"amount"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(invoice.AncestorIDs) != 1 || invoice.AncestorIDs[0] != "type" || len(invoice.Effective.FieldIds) != 2 || invoice.Effective.FieldIds[0] != "title" || len(invoice.Effective.Rules) != 1 {
		t.Fatalf("inherited schema: %+v", invoice)
	}
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "clash", Key: "clash", Name: "Clash", ParentID: &parent, FieldIds: []string{"amount-copy"}}); err != nil {
		t.Fatal(err)
	}
	// The catalog type cannot gain a field key a descendant already has.
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: "type", FieldIds: []string{"title", "amount"}}, 2); err == nil {
		t.Fatal("parent field key clashing with a subtype was accepted")
	}
	listType, _, err := r.Attach(ctx, f.Destination, invoice.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "grandchild", Key: "grandchild", Name: "Grandchild", ParentID: &listType.ID}); err == nil {
		t.Fatal("a list type became a parent")
	}
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: listType.ID, FieldIds: []string{"due"}, HiddenFieldIds: []string{"amount"}}, listType.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: listType.ID, FieldIds: []string{"due"}, HiddenFieldIds: []string{"due"}}, listType.Version+1); !errors.Is(err, dberr.ErrInvalidReference) {
		t.Fatalf("hid an own field: %v", err)
	}
	listType, err = r.ContentType(ctx, listType.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := listType.Effective; len(got.FieldIds) != 3 || got.FieldIds[2] != "due" || len(got.HiddenFieldIds) != 1 || got.HiddenFieldIds[0] != "amount" {
		t.Fatalf("list type schema: %+v", got)
	}
	if len(listType.AncestorIDs) != 2 || listType.AncestorIDs[0] != "invoice" || listType.AncestorIDs[1] != "type" {
		t.Fatalf("list type lineage: %v", listType.AncestorIDs)
	}
	// A catalog change reaches every descendant and changes its ETag.
	invoice, err = r.SaveContentType(ctx, catalog.ContentType{ID: "invoice", Name: "Bill", FieldIds: []string{"amount", "note"}}, invoice.Version)
	if err != nil || invoice.Version != 2 || len(invoice.Effective.FieldIds) != 3 {
		t.Fatal(invoice, err)
	}
	updated, err := r.ContentType(ctx, listType.ID)
	if err != nil || updated.Name != "Bill" || len(updated.Effective.FieldIds) != 4 || updated.Version != listType.Version+1 {
		t.Fatalf("descendant after a parent change: %+v %v", updated, err)
	}
	fieldsOf, err := r.Fields(ctx, catalog.FieldFilter{CollectionID: f.Destination})
	if err != nil || len(fieldsOf) != 4 {
		t.Fatalf("collection fields include inherited ones: %+v %v", fieldsOf, err)
	}
	// A descendant stores neither own fields its parent gains nor hidden
	// fields its parent loses.
	if _, err = r.SaveContentType(ctx, catalog.ContentType{ID: "invoice", Name: "Bill", FieldIds: []string{"note", "due"}}, invoice.Version); err != nil {
		t.Fatal(err)
	}
	var own, hidden string
	if err = s.SQLDB().QueryRowContext(ctx, `SELECT field_ids, hidden_field_ids FROM content_types WHERE id = '`+listType.ID+`'`).Scan(&own, &hidden); err != nil {
		t.Fatal(err)
	}
	if !jsonutil.Equal([]byte(own), []byte(`[]`)) || !jsonutil.Equal([]byte(hidden), []byte(`[]`)) {
		t.Fatalf("descendant keeps stale fields: %s %s", own, hidden)
	}
	if updated, err = r.ContentType(ctx, listType.ID); err != nil || len(updated.Effective.FieldIds) != 3 || updated.Version != listType.Version+2 {
		t.Fatalf("descendant after its fields moved up: %+v %v", updated, err)
	}
	descendants, err := r.Descendants(ctx, []string{"type"})
	if err != nil || len(descendants) != 6 {
		t.Fatalf("descendants: %v %v", descendants, err)
	}
	if collections, err := r.AttachedCollections(ctx, []string{"invoice"}); err != nil || len(collections) != 1 || collections[0] != f.Destination {
		t.Fatalf("collections of a subtype: %v %v", collections, err)
	}
	// The deepest catalog type leaves room for its list types.
	deepest := "invoice"
	for i := 2; i < catalog.MaxDepth; i++ {
		id := fmt.Sprintf("level-%d", i)
		if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: id, Key: id, Name: id, ParentID: &deepest}); err != nil {
			t.Fatal(err)
		}
		deepest = id
	}
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "too-deep", Key: "too-deep", Name: "Too deep", ParentID: &deepest}); err == nil {
		t.Fatal("catalog type below the deepest level")
	}
	if deep, _, err := r.Attach(ctx, f.Destination, deepest, false); err != nil || len(deep.AncestorIDs) != catalog.MaxDepth {
		t.Fatalf("deepest catalog type attached: %+v %v", deep, err)
	}
}
