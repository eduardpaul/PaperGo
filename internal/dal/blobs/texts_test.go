package blobs_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
)

func TestBlobTexts_SQLite(t *testing.T)   { testBlobTexts(t, daltest.SQLiteStore(t)) }
func TestBlobTexts_Postgres(t *testing.T) { testBlobTexts(t, daltest.PostgresStore(t)) }

func testBlobTexts(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	in := blobs.TextInput{SHA256: strings.Repeat("a", 64), Settings: "extractor-v1", ObjectKey: "text-first", ObjectSize: 20, ObjectSHA256: strings.Repeat("b", 64), Pages: 2, Chars: 40, Extractor: "test"}
	if _, err := s.Blobs().SaveText(ctx, in); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("text without live bytes accepted: %v", err)
	}
	blob, err := s.Blobs().AddBlob(ctx, blobs.Blob{ID: "blob", ItemID: f.Item, ObjectKey: "source-object", Filename: "file.pdf", ContentType: blobs.MediaPDF, Languages: "", Sha256: in.SHA256, Size: 100})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.Blobs().SaveText(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ObjectKey = "losing-object"
	replay, err := s.Blobs().SaveText(ctx, in)
	if err != nil || replay.ID != first.ID || replay.ObjectKey != "text-first" {
		t.Fatalf("first writer lost: %+v %v", replay, err)
	}
	in.Settings, in.ObjectKey = "extractor-v2", "text-second"
	second, err := s.Blobs().SaveText(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.Blobs().LatestText(ctx, in.SHA256)
	if err != nil || latest.ID != second.ID {
		t.Fatalf("latest text: %+v %v", latest, err)
	}
	found, err := s.Blobs().Text(ctx, first.ID)
	if err != nil || found.Sha256 != in.SHA256 || found.Settings != "extractor-v1" || found.Pages != 2 || string(found.Models) != "[]" {
		t.Fatalf("metadata roundtrip: %+v %v", found, err)
	}
	objects, err := s.Blobs().StoredObjects(ctx)
	if err != nil || len(objects) != 3 {
		t.Fatalf("stored objects: %+v %v", objects, err)
	}
	wantKeys := []string{"source-object", "text-first", "text-second"}
	for i, object := range objects {
		if object.Key != wantKeys[i] {
			t.Fatalf("stored object order: %+v", objects)
		}
	}
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	in.Settings = "rolled-back"
	if _, err = s.WithTx(tx).Blobs().SaveText(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Blobs().TextCount(ctx, in.SHA256); err != nil || n != 2 {
		t.Fatalf("rollback lost: %d %v", n, err)
	}
	if err := s.Blobs().CollectTexts(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Blobs().TextCount(ctx, ""); err != nil || n != 2 {
		t.Fatalf("live text collected: %d %v", n, err)
	}
	daltest.Delete(t, s, "blobs", daltest.Row{"id": blob.ID})
	if err := s.Blobs().CollectTexts(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.Blobs().TextCount(ctx, ""); err != nil || n != 0 {
		t.Fatalf("orphan text retained: %d %v", n, err)
	}
	deletions, err := s.Blobs().Deletions(ctx, 10)
	if err != nil || len(deletions) != 2 {
		t.Fatalf("orphan objects not queued: %+v %v", deletions, err)
	}
}
