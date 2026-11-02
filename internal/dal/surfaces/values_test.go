package surfaces_test

import (
	"context"
	"encoding/json"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
)

// Projecting a revision again rewrites only the values that change: equal
// values keep their rows, and group counts move by the changed choices.
func TestProjectWritesOnlyChangedValues(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		ws, err := s.Resources().Create(ctx, resources.New{ID: "ws-values", WorkspaceID: "ws-values", Kind: refs.KindWorkspace, Name: "Values", CreatedBy: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		lib, err := s.Resources().Create(ctx, resources.New{ID: "lib-values", WorkspaceID: ws.ID, ParentID: ws.ID, Kind: refs.KindLibrary, Name: "Invoices", CreatedBy: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		stage, err := s.Catalog().CreateField(ctx, catalog.FieldDefinition{ID: "fd-v-stage", Key: "stage", Label: "Stage", Type: catalog.FieldChoice, Indexed: true, Choices: []string{"open", "paid"}})
		if err != nil {
			t.Fatal(err)
		}
		vendor, err := s.Catalog().CreateField(ctx, catalog.FieldDefinition{ID: "fd-v-vendor", Key: "vendor", Label: "Vendor", Type: catalog.FieldText, Indexed: true})
		if err != nil {
			t.Fatal(err)
		}
		ct, err := s.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "ct-values", Key: "values", Name: "Values", FieldIds: []string{stage.ID, vendor.ID}})
		if err != nil {
			t.Fatal(err)
		}
		lt, _, err := s.Catalog().Attach(ctx, lib.ID, ct.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		sr, err := s.Catalog().RecordSchema(ctx, lib.ID, "u1", json.RawMessage(`{"content_types":[{"id":"`+lt.ID+`"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		item, err := s.Resources().Create(ctx, resources.New{ID: "item-values", WorkspaceID: ws.ID, ParentID: lib.ID, ContainerID: lib.ID, ContentTypeID: lt.ID, Kind: refs.KindItem, Name: "INV", CreatedBy: "u1"})
		if err != nil {
			t.Fatal(err)
		}
		project := func(stage, vendor string) map[string]string {
			t.Helper()
			rev, err := daltest.Revise(ctx, s, revisions.Revision{ItemID: item.ID, SchemaRevisionID: sr.ID, Name: "INV", Payload: json.RawMessage(`{}`), CreatedBy: "u1"})
			if err != nil {
				t.Fatal(err)
			}
			fields := []surfaces.FieldValue{{FieldKey: "stage", FieldType: catalog.FieldChoice, ValueText: &stage}}
			if vendor != "" {
				fields = append(fields, surfaces.FieldValue{FieldKey: "vendor", FieldType: catalog.FieldText, ValueText: &vendor})
			}
			if _, err := s.Surfaces().Project(ctx, surfaces.Projection{ItemID: item.ID, Surface: refs.SurfacePublished, RevisionID: rev.ID, ModifiedBy: "u1", Fields: fields}); err != nil {
				t.Fatal(err)
			}
			values, err := s.Surfaces().FieldValues(ctx, surfaces.ValueFilter{ItemID: item.ID})
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]string{}
			for _, v := range values {
				ids[v.FieldKey+"="+*v.ValueText] = v.ID
			}
			return ids
		}
		counts := func(want map[string]int) {
			t.Helper()
			c, err := s.Surfaces().Counts(ctx, lib.ID, refs.SurfacePublished, "stage")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]int{}
			for _, v := range c.Values {
				got[v.Value] = v.Count
			}
			if c.Total != 1 || len(got) != len(want) {
				t.Fatalf("counts = %+v, want %v", c, want)
			}
			for k, n := range want {
				if got[k] != n {
					t.Fatalf("counts = %+v, want %v", c, want)
				}
			}
		}

		first := project("open", "Power Co")
		counts(map[string]int{"open": 1})
		second := project("paid", "Power Co")
		if len(second) != 2 || second["vendor=Power Co"] != first["vendor=Power Co"] || second["stage=paid"] == "" {
			t.Fatalf("values after changing stage = %v, before %v", second, first)
		}
		counts(map[string]int{"paid": 1})
		third := project("paid", "")
		if len(third) != 1 || third["stage=paid"] != second["stage=paid"] {
			t.Fatalf("values after clearing vendor = %v, before %v", third, second)
		}
		counts(map[string]int{"paid": 1})
	})
}
