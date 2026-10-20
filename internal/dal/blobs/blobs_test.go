package blobs_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
)

func TestBlobPreviewsAndDeletions_SQLite(t *testing.T) {
	testBlobPreviewsAndDeletions(t, daltest.SQLiteStore(t))
}
func TestBlobPreviewsAndDeletions_Postgres(t *testing.T) {
	testBlobPreviewsAndDeletions(t, daltest.PostgresStore(t))
}
func testBlobPreviewsAndDeletions(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	sha := strings.Repeat("b", 64)
	if err := s.Blobs().SavePreview(ctx, blobs.Preview{SHA256: sha}); !dberr.IsNotFound(err) {
		t.Fatalf("preview of unheld bytes: %v", err)
	}
	packages := blobs.Packages{ImportKind: "import", ExportKind: "export"}
	if ok, err := s.Blobs().Referenced(ctx, "object", packages); err != nil || ok {
		t.Fatalf("unused object referenced: %v %v", ok, err)
	}
	if _, err := s.Blobs().AddBlob(ctx, blobs.Blob{ID: "blob", ItemID: f.Item, ObjectKey: "object", Filename: "a.pdf", ContentType: blobs.MediaPDF, Languages: "", Sha256: sha, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Blobs().Referenced(ctx, "object", packages); err != nil || !ok {
		t.Fatalf("blob object not referenced: %v %v", ok, err)
	}
	export := func(key string, expires time.Time) {
		t.Helper()
		now := time.Now()
		if _, _, err := s.Operations().Accept(ctx, operations.Operation{Kind: "export", CreatedBy: "alice"}, now); err != nil {
			t.Fatal(err)
		}
		claimed, err := s.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{"export"}, Now: now, Limit: 1}, Holder: "test", TTL: time.Minute})
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim export: %v %v", claimed, err)
		}
		op := claimed[0]
		op.Status, op.Result = operations.StatusSucceeded, json.RawMessage(`{"object_key":"`+key+`","expires_at":"`+expires.UTC().Format(time.RFC3339)+`"}`)
		if err := s.Operations().Finish(ctx, &op, now); err != nil {
			t.Fatal(err)
		}
	}
	export("package", time.Now().Add(time.Hour))
	if ok, err := s.Blobs().Referenced(ctx, "package", packages); err != nil || !ok {
		t.Fatalf("export package not referenced: %v %v", ok, err)
	}
	export("stale", time.Now().Add(-time.Hour))
	if ok, err := s.Blobs().Referenced(ctx, "stale", packages); err != nil || ok {
		t.Fatalf("expired export package referenced: %v %v", ok, err)
	}
	if p, err := s.Blobs().Preview(ctx, sha); err != nil || p != nil {
		t.Fatalf("missing preview: %+v %v", p, err)
	}
	if err := s.Blobs().SavePreview(ctx, blobs.Preview{SHA256: sha}); err != nil {
		t.Fatal(err)
	}
	ready := blobs.Preview{SHA256: sha, ObjectKey: "preview", ObjectSHA256: strings.Repeat("c", 64), ObjectSize: 5, Width: 10, Height: 20}
	if err := s.Blobs().SavePreview(ctx, ready); err != nil {
		t.Fatal(err)
	}
	// A ready preview wins over a later failure.
	if err := s.Blobs().SavePreview(ctx, blobs.Preview{SHA256: sha}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Blobs().Preview(ctx, sha)
	if err != nil || p == nil || p.Status != blobs.PreviewReady || p.ObjectKey != "preview" || p.Width != 10 || p.Height != 20 {
		t.Fatalf("ready preview: %+v %v", p, err)
	}
	if err := s.Blobs().CollectPreviews(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Blobs().Preview(ctx, sha); err != nil || p == nil {
		t.Fatalf("held preview collected: %+v %v", p, err)
	}
	if err := s.Blobs().QueueItemObjects(ctx, f.Item); err != nil {
		t.Fatal(err)
	}
	if err := s.Blobs().QueueDeletion(ctx, "object"); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Blobs().Deletions(ctx, 10)
	if err != nil || len(queued) != 1 || queued[0].ID != "blob" || queued[0].ObjectKey != "object" {
		t.Fatalf("queued deletions: %+v %v", queued, err)
	}
	after := time.Now()
	if _, err := s.Blobs().SetAvailability(ctx, "blob", blobs.AvailabilityPending, &after); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Blobs().SetAvailability(ctx, "blob", blobs.AvailabilityReclaimed, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Blobs().CollectPreviews(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Blobs().Preview(ctx, sha); err != nil || p != nil {
		t.Fatalf("orphan preview retained: %+v %v", p, err)
	}
	queued, err = s.Blobs().Deletions(ctx, 10)
	if err != nil || len(queued) != 2 || queued[1].ObjectKey != "preview" {
		t.Fatalf("orphan preview object not queued: %+v %v", queued, err)
	}
	for _, d := range queued {
		if err := s.Blobs().Dequeue(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	if queued, err := s.Blobs().Deletions(ctx, 10); err != nil || len(queued) != 0 {
		t.Fatalf("dequeued deletions: %+v %v", queued, err)
	}
}
