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

func TestFieldRepository_SQLite(t *testing.T)   { testFieldRepository(t, daltest.SQLiteStore(t)) }
func TestFieldRepository_Postgres(t *testing.T) { testFieldRepository(t, daltest.PostgresStore(t)) }
func testFieldRepository(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Catalog()
	field, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "amount", Key: "amount", Label: "Amount", Type: catalog.FieldInteger, Indexed: true,
		Options: model.FieldOptions{DefaultValue: json.RawMessage(`9007199254740993`)}})
	if err != nil {
		t.Fatal(err)
	}
	if field, err = r.Field(ctx, field.ID); err != nil {
		t.Fatal(err)
	}
	if !jsonutil.Equal(field.Options.DefaultValue, []byte(`9007199254740993`)) {
		t.Fatalf("default precision lost: %s", field.Options.DefaultValue)
	}
	if _, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "outside", Key: "outside", Label: "Outside", Type: catalog.FieldText}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SaveContentType(ctx, catalog.ContentType{ID: "type", Name: "Document", FieldIds: []string{field.ID}}, 1); err != nil {
		t.Fatal(err)
	}
	scoped, err := s.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: f.Source})
	if err != nil || len(scoped) != 1 || scoped[0].ID != field.ID {
		t.Fatalf("collection field scope: %v %v", scoped, err)
	}
	if rows, err := s.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: f.Source, IDs: []string{"outside"}}); err != nil || len(rows) != 0 {
		t.Fatalf("unattached field escaped scope: %v %v", rows, err)
	}
	if rows, err := s.Catalog().Fields(ctx, catalog.FieldFilter{IDs: []string{}}); err != nil || len(rows) != 0 {
		t.Fatalf("empty selection widened: %v %v", rows, err)
	}
	if n, err := s.Catalog().CountFields(ctx, catalog.FieldFilter{}); err != nil || n != 2 {
		t.Fatalf("catalog fields: %d %v", n, err)
	}
	if _, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "invalid", Key: "invalid", Label: "Invalid", Type: catalog.FieldText, Scale: 2}); !errors.Is(err, dberr.ErrCheckViolation) {
		t.Fatalf("scale on a text field accepted: %v", err)
	}
	if _, err := r.CreateField(ctx, catalog.FieldDefinition{ID: "invalid", Key: "invalid", Label: "Invalid", Type: catalog.FieldText, Options: model.FieldOptions{Multiple: true}}); !errors.Is(err, dberr.ErrCheckViolation) {
		t.Fatalf("multiple text field accepted: %v", err)
	}
	if _, err := s.Catalog().Field(ctx, "invalid"); !dberr.IsNotFound(err) {
		t.Fatalf("failed creation committed: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() {
			change := *field
			change.Label, change.Required = "Updated", true
			_, errs[i] = r.SaveField(ctx, change, field.Version)
		})
	}
	wg.Wait()
	won, lost := 0, 0
	for _, err := range errs {
		if err == nil {
			won++
		} else if errors.Is(err, dberr.ErrConcurrentUpdate) {
			lost++
		} else {
			t.Fatal(err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("optimistic update: %d winners, %d stale", won, lost)
	}
	updated, err := s.Catalog().Field(ctx, field.ID)
	if err != nil || updated.Version != 2 || updated.Label != "Updated" {
		t.Fatalf("updated field: %+v %v", updated, err)
	}
	change := *updated
	change.Label, change.Options.Multiple = "Changed", true
	if _, err := r.SaveField(ctx, change, updated.Version); !errors.Is(err, dberr.ErrImmutable) {
		t.Fatalf("cardinality changed: %v", err)
	}
	retained, err := s.Catalog().Field(ctx, field.ID)
	if err != nil || retained.Version != updated.Version || retained.Label != updated.Label {
		t.Fatalf("failed update committed: %+v %v", retained, err)
	}
	if err := s.Catalog().SetIndexStatus(ctx, []string{field.ID}, catalog.IndexBuilding); err != nil {
		t.Fatal(err)
	}
	found, err := s.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: f.Source, Keys: []string{"amount"}})
	if err != nil || found.IndexStatus != catalog.IndexBuilding || found.Version != 2 {
		t.Fatalf("index state: %+v %v", found, err)
	}
	// Storage identity remains fixed while public settings evolve.
	if found.Key != "amount" || found.Type != catalog.FieldInteger || found.Scale != 0 {
		t.Fatalf("field identity: %+v", found)
	}
}
