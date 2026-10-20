package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
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
	if _, err := r.Attach(ctx, f.Source, typ.ID, false); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{CollectionID: f.Source, FieldID: field.ID})
	if err != nil || len(rows) != 1 || rows[0].ID != typ.ID {
		t.Fatalf("JSON membership and attachment scope: %+v %v", rows, err)
	}
	found, err := s.Catalog().OneContentType(ctx, catalog.ContentTypeFilter{TemplateKey: "recipe"})
	if err != nil || found.ID != typ.ID {
		t.Fatalf("template lookup: %+v %v", found, err)
	}
	if selected, err := s.Catalog().CollectionContentType(ctx, f.Source, typ.ID); err != nil || selected.ID != typ.ID {
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
	if _, err := r.CreateContentType(ctx, catalog.ContentType{ID: "memo-copy", Key: "memo", Name: "Memo copy"}); err != nil {
		t.Fatal(err)
	}
	defaults := func() string {
		t.Helper()
		typ, err := r.CollectionContentType(ctx, f.Destination, "")
		if err != nil {
			t.Fatal(err)
		}
		return typ.ID
	}
	// The fixture attached "type" as the default; others join without it.
	if changed, err := r.Attach(ctx, f.Destination, "memo", false); err != nil || !changed || defaults() != "type" {
		t.Fatalf("attach: %v %v %s", changed, err, defaults())
	}
	if changed, err := r.Attach(ctx, f.Destination, "memo", false); err != nil || changed {
		t.Fatalf("reattach: %v %v", changed, err)
	}
	if _, err := r.Attach(ctx, f.Destination, "memo-copy", false); !errors.Is(err, dberr.ErrUniqueViolation) {
		t.Fatalf("duplicate key: %v", err)
	}
	if _, err := r.Attach(ctx, f.Folder, "memo", false); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("attached to a folder: %v", err)
	}
	if _, err := r.Attach(ctx, f.Destination, "letter", true); err != nil || defaults() != "letter" {
		t.Fatalf("new default: %v %s", err, defaults())
	}
	if ok, err := r.Accepts(ctx, f.Destination, "memo"); err != nil || !ok {
		t.Fatalf("accepts: %v %v", ok, err)
	}
	collections, err := r.AttachedCollections(ctx, []string{"letter", "memo"})
	if err != nil || len(collections) != 1 || collections[0] != f.Destination {
		t.Fatalf("attached collections: %v %v", collections, err)
	}
	// Detaching the default promotes the remaining type with the lowest id.
	if err := r.Detach(ctx, f.Destination, "letter"); err != nil || defaults() != "memo" {
		t.Fatalf("detach default: %v %s", err, defaults())
	}
	if _, err := r.Attach(ctx, f.Source, "letter", false); err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(ctx, f.Source, "type"); !errors.Is(err, catalog.ErrContentTypeInUse) {
		t.Fatalf("detached a type in use: %v", err)
	}
	if err := r.Detach(ctx, f.Destination, "memo"); err != nil {
		t.Fatal(err)
	}
	if err := r.Detach(ctx, f.Destination, "type"); !errors.Is(err, catalog.ErrLastContentType) {
		t.Fatalf("detached the last type: %v", err)
	}
	if err := r.Detach(ctx, f.Destination, "memo"); !dberr.IsNotFound(err) {
		t.Fatalf("detached twice: %v", err)
	}
}
