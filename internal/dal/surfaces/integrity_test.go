package surfaces_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/shopspring/decimal"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	"testing"
)

func TestRevisionsSurfacesDynamicQueryAndGroupCounts(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		ws, err := s.Resources().Create(ctx, resources.New{
			ID:          "ws-surf",
			WorkspaceID: "ws-surf",
			Kind:        refs.KindWorkspace,
			Name:        "Finance",
			CreatedBy:   "u1",
		})
		if err != nil {
			t.Fatalf("CreateResource ws: %v", err)
		}
		lib, err := s.Resources().Create(ctx, resources.New{
			ID:          "lib-surf",
			WorkspaceID: ws.ID,
			ParentID:    ws.ID,
			Kind:        refs.KindLibrary,
			Name:        "Invoices",
			CreatedBy:   "u1",
		})
		if err != nil {
			t.Fatalf("CreateResource lib: %v", err)
		}

		// Verify unvalidated JSON reference array fix on ContentType
		_, err = s.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "ct-bad", Key: "bad", Name: "Bad", FieldIds: []string{"non-existent-field-id"}})
		if !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("expected ErrInvalidReference for unknown field_id in content_type, got %v", err)
		}

		fdStage, err := s.Catalog().CreateField(ctx, catalog.FieldDefinition{ID: "fd-stage", Key: "stage", Label: "Stage", Type: catalog.FieldChoice, Required: true, Indexed: true, Choices: []string{"open", "paid"}})
		if err != nil {
			t.Fatalf("CreateFieldDefinition stage: %v", err)
		}
		fdAmount, err := s.Catalog().CreateField(ctx, catalog.FieldDefinition{ID: "fd-amount", Key: "amount", Label: "Amount", Type: catalog.FieldCurrency, Indexed: true})
		if err != nil {
			t.Fatalf("CreateFieldDefinition amount: %v", err)
		}
		ct, err := s.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "ct-inv", Key: "invoice", Name: "Invoice", FieldIds: []string{fdStage.ID, fdAmount.ID}})
		if err != nil {
			t.Fatalf("CreateContentType: %v", err)
		}

		// Deleting a field definition that is referenced by ct must fail with ErrForeignKeyViolation
		err = s.Catalog().DeleteField(ctx, fdStage.ID)
		if !errors.Is(err, dberr.ErrForeignKeyViolation) {
			t.Fatalf("expected ErrForeignKeyViolation when deleting referenced field definition, got %v", err)
		}

		lt, _, err := s.Catalog().Attach(ctx, lib.ID, ct.ID, true)
		if err != nil {
			t.Fatalf("Attach: %v", err)
		}

		schemaDef := json.RawMessage(`{"content_types":[{"id":"` + lt.ID + `"}]}`)
		sr, err := s.Catalog().RecordSchema(ctx, lib.ID, "u1", schemaDef)
		if err != nil {
			t.Fatalf("RecordSchema: %v", err)
		}

		item, err := s.Resources().Create(ctx, resources.New{
			ID:            "item-inv-1",
			WorkspaceID:   ws.ID,
			ParentID:      lib.ID,
			ContainerID:   lib.ID,
			ContentTypeID: lt.ID,
			Kind:          refs.KindItem,
			Name:          "INV-001",
			CreatedBy:     "u1",
		})
		if err != nil {
			t.Fatalf("CreateResource item: %v", err)
		}

		sha := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		blob, err := s.Blobs().AddBlob(ctx, blobs.Blob{ID: "blob-1", ItemID: item.ID, ObjectKey: "obj/inv1.pdf", Filename: "inv1.pdf", ContentType: blobs.MediaPDF, Languages: "en", Sha256: sha, Size: 4096})
		if err != nil {
			t.Fatalf("AddBlob: %v", err)
		}

		rev, err := daltest.Revise(ctx, s, revisions.Revision{
			ID:               "rev-1",
			ItemID:           item.ID,
			SchemaRevisionID: sr.ID,
			Name:             "INV-001",
			Keywords:         []string{"q4-tax"},
			Payload:          json.RawMessage(`{"stage":"open","amount":1250.50}`),
			BlobID:           &blob.ID,
			Filename:         "inv1.pdf",
			CreatedBy:        "u1",
		})
		if err != nil {
			t.Fatalf("CreateItemRevision: %v", err)
		}

		// Project onto head surface with choice and numeric field values
		openVal := "open"
		amtVal := decimal.NewFromFloat(1250.50)
		if _, err := s.Surfaces().Project(ctx, surfaces.Projection{
			ItemID:     item.ID,
			Surface:    refs.SurfaceHead,
			RevisionID: rev.ID,
			ModifiedBy: "u1",
			Fields: []surfaces.FieldValue{
				{FieldKey: "stage", FieldType: catalog.FieldChoice, ValueText: &openVal},
				{FieldKey: "amount", FieldType: catalog.FieldCurrency, ValueNumeric: &amtVal},
			},
		}); err != nil {
			t.Fatalf("Project: %v", err)
		}
		matched, err := s.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: lib.ID, Surface: refs.SurfaceHead})
		if err != nil {
			t.Fatalf("Surfaces: %v", err)
		}
		if len(matched) != 1 || matched[0].ItemID != item.ID || matched[0].Name != "INV-001" {
			t.Fatalf("head surfaces = %+v", matched)
		}

		// One collection total and one count of the choice value.
		counts, err := s.Surfaces().Counts(ctx, lib.ID, refs.SurfaceHead, "stage")
		if err != nil {
			t.Fatalf("Counts: %v", err)
		}
		if counts.Total != 1 || len(counts.Values) != 1 || counts.Values[0].Value != "open" || counts.Values[0].Count != 1 {
			t.Fatalf("counts = %+v", counts)
		}

		// Removing the surface removes its counts instead of leaving zeros.
		if err := s.Surfaces().Remove(ctx, item.ID, refs.SurfaceHead); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		counts, err = s.Surfaces().Counts(ctx, lib.ID, refs.SurfaceHead, "stage")
		if err != nil {
			t.Fatalf("Counts: %v", err)
		}
		if counts.Total != 0 || len(counts.Values) != 0 {
			t.Fatalf("counts after removal = %+v", counts)
		}
	})
}

func TestProjectionLifecycle(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		f := daltest.NewFixture(t, newStore(t))
		s := f.Store
		if _, err := s.Catalog().ClaimKey(ctx, catalog.BusinessKey{CollectionID: f.Source, ItemID: f.Item, FieldKey: "external_id", Value: "42"}); err != nil {
			t.Fatal(err)
		}
		daltest.AssertCounts(t, f, f.Source, 4)
		// A transfer leaves projections and business keys behind; the caller
		// projects the item again in its new collection.
		if _, err := s.Resources().Transfer(ctx, f.Item, f.Destination, f.Destination, "item", nil, "alice"); err != nil {
			t.Fatal(err)
		}
		daltest.AssertCounts(t, f, f.Source, 0)
		daltest.AssertCounts(t, f, f.Destination, 0)
		keys, err := s.Catalog().Keys(ctx, catalog.KeyFilter{ItemID: f.Item})
		if err != nil || len(keys) != 0 {
			t.Fatalf("business key kept: %+v, %v", keys, err)
		}
		daltest.Project(t, f)
		daltest.AssertCounts(t, f, f.Destination, 4)
		fields, err := s.Surfaces().FieldValues(ctx, surfaces.ValueFilter{ContainerID: f.Destination})
		if err != nil || len(fields) != 2 {
			t.Fatalf("field projection: %+v, %v", fields, err)
		}
		if err := daltest.Remove(ctx, s, f.Item, "alice"); err != nil {
			t.Fatal(err)
		}
		daltest.AssertCounts(t, f, f.Destination, 0)
		if _, err := s.Surfaces().Project(ctx, surfaces.Projection{ItemID: f.Item, Surface: refs.SurfaceHead, RevisionID: f.Revision}); !errors.Is(err, dberr.ErrImmutable) {
			t.Fatalf("project tombstone: %v", err)
		}
		if err := daltest.Restore(ctx, s, f.Item, "item", "alice"); err != nil {
			t.Fatal(err)
		}
		daltest.AssertCounts(t, f, f.Destination, 0)
		daltest.Project(t, f)
		daltest.AssertCounts(t, f, f.Destination, 4)
		fields, err = s.Surfaces().FieldValues(ctx, surfaces.ValueFilter{ContainerID: f.Destination, FieldKey: "stage"})
		if err != nil || len(fields) != 2 {
			t.Fatalf("restored projection lost fields: %+v, %v", fields, err)
		}
		if err := s.Resources().Purge(ctx, f.Item); err != nil {
			t.Fatal(err)
		}
		daltest.AssertCounts(t, f, f.Destination, 0)
		fields, err = s.Surfaces().FieldValues(ctx, surfaces.ValueFilter{})
		if err != nil || len(fields) != 0 {
			t.Fatalf("orphaned field values: %+v, %v", fields, err)
		}
	})
}
