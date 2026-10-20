package resources_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
	"testing"
	"time"
)

func TestResourceChanges_SQLite(t *testing.T)   { testResourceChanges(t, daltest.SQLiteStore(t)) }
func TestResourceChanges_Postgres(t *testing.T) { testResourceChanges(t, daltest.PostgresStore(t)) }
func testResourceChanges(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	original, err := s.Resources().Resource(ctx, f.Item)
	if err != nil {
		t.Fatal(err)
	}
	name := "Changed"
	words := []string{"finance"}
	if err := s.Resources().Change(ctx, f.Item, original.Version, "alice", resources.Changes{Name: &name, Keywords: &words}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.Resources().Resource(ctx, f.Item)
	if err != nil || changed.Name != name || changed.Version != original.Version+1 {
		t.Fatalf("resource change: %+v %v", changed, err)
	}
	if len(changed.Keywords) != 1 {
		t.Fatalf("keywords not resolved: %+v", changed.Keywords)
	}
	if indexed := daltest.Count(t, s, "resource_keywords", daltest.Row{"resource_id": f.Item, "keyword": changed.Keywords[0]}); indexed != 1 {
		t.Fatalf("keyword projection: %d", indexed)
	}
	stale := "stale"
	if err := s.Resources().Change(ctx, f.Item, original.Version, "alice", resources.Changes{Name: &stale}); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale writer: %v", err)
	}
	invalidParent := f.Workspace
	if err := s.Resources().Change(ctx, f.Item, changed.Version, "alice", resources.Changes{Name: &stale, ParentID: &invalidParent}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("invalid move: %v", err)
	}
	retained, err := s.Resources().Resource(ctx, f.Item)
	if err != nil || retained.Name != name || retained.Version != changed.Version {
		t.Fatalf("failed write escaped transaction: %+v %v", retained, err)
	}
}

func TestReindexSurfaceFields_SQLite(t *testing.T) {
	testReindexSurfaceFields(t, daltest.SQLiteStore(t))
}
func TestReindexSurfaceFields_Postgres(t *testing.T) {
	testReindexSurfaceFields(t, daltest.PostgresStore(t))
}
func testReindexSurfaceFields(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	rows, err := s.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: f.Source, Surface: refs.SurfacePublished})
	if err != nil || len(rows) != 1 {
		t.Fatalf("surface: %v %v", rows, err)
	}
	at := time.Date(2026, 10, 8, 12, 30, 0, 123456000, time.FixedZone("offset", 3600))
	if err := s.Surfaces().Touch(ctx, []string{f.Item}, at, "editor"); err != nil {
		t.Fatal(err)
	}
	touched, err := s.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: f.Source})
	if err != nil || len(touched) != 2 {
		t.Fatalf("touched surfaces: %+v %v", touched, err)
	}
	for _, surface := range touched {
		if !surface.ModifiedAt.Equal(at) || surface.ModifiedBy != "editor" {
			t.Fatalf("native surface timestamp: %+v", surface)
		}
	}

	closed := "closed"
	unrelated := "leave alone"
	if err := s.Surfaces().Reindex(ctx, rows[0].ID, []string{"memo"}, []surfaces.FieldValue{{FieldKey: "memo", FieldType: catalog.FieldText, ValueText: &unrelated}}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Surfaces().Reindex(ctx, rows[0].ID, []string{"stage"}, []surfaces.FieldValue{{FieldKey: "stage", FieldType: catalog.FieldChoice, ValueText: &closed}}); err != nil {
			t.Fatal(err)
		}
	}
	values, err := s.Surfaces().FieldValues(ctx, surfaces.ValueFilter{SurfaceID: rows[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]string{}
	for _, v := range values {
		kept[v.FieldKey] = *v.ValueText
	}
	if len(values) != 2 || kept["memo"] != unrelated || kept["stage"] != closed {
		t.Fatalf("reindex lost another field: %+v", kept)
	}
	counts, err := s.Surfaces().Counts(ctx, f.Source, refs.SurfacePublished, "stage")
	if err != nil || len(counts.Values) != 1 || counts.Values[0].Value != closed || counts.Values[0].Count != 1 {
		t.Fatalf("reindex counter drift: %+v %v", counts, err)
	}
}
