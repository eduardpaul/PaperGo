package blobs_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/dberr"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"papergo/internal/model"
)

func TestBlobFiles_SQLite(t *testing.T)   { testBlobFiles(t, daltest.SQLiteStore(t)) }
func TestBlobFiles_Postgres(t *testing.T) { testBlobFiles(t, daltest.PostgresStore(t)) }

func testBlobFiles(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	r := s.Blobs()
	sha := strings.Repeat("c", 64)
	first, err := r.AddBlob(ctx, blobs.Blob{ItemID: f.Item, ObjectKey: "first", Filename: "a.pdf", ContentType: blobs.MediaPDF, Sha256: sha, Size: 10})
	if err != nil || first.Version != 1 || first.Availability != blobs.AvailabilityAvailable {
		t.Fatalf("first blob: %+v %v", first, err)
	}
	// The same bytes reuse the stored object, and the new one is queued.
	second, err := r.AddBlob(ctx, blobs.Blob{ItemID: f.Item, ObjectKey: "second", Filename: "b.pdf", ContentType: blobs.MediaPDF, Sha256: sha, Size: 10})
	if err != nil || second.Version != 2 || second.ObjectKey != "first" {
		t.Fatalf("shared blob: %+v %v", second, err)
	}
	deletions, err := r.Deletions(ctx, 10)
	if err != nil || len(deletions) != 1 || deletions[0].ObjectKey != "second" {
		t.Fatalf("duplicate object not queued: %+v %v", deletions, err)
	}
	if _, err = r.AddBlob(ctx, blobs.Blob{ItemID: f.Folder, ObjectKey: "x", Filename: "x.pdf", ContentType: blobs.MediaPDF, Sha256: sha, Size: 10}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("blob on a folder: %v", err)
	}
	if _, err = r.AddBlob(ctx, blobs.Blob{ItemID: f.Item, ObjectKey: "x", Filename: "x.txt", ContentType: "not a media type", Sha256: sha, Size: 10}); !errors.Is(err, dberr.ErrCheckViolation) {
		t.Fatalf("malformed media type: %v", err)
	}
	if found, err := r.ItemVersion(ctx, f.Item, 1); err != nil || found.ID != first.ID {
		t.Fatalf("item version: %+v %v", found, err)
	}
	newest, err := r.Blobs(ctx, blobs.BlobFilter{ItemID: f.Item, Order: blobs.NewestFirst})
	if err != nil || len(newest) != 2 || newest[0].ID != second.ID {
		t.Fatalf("newest first: %+v %v", newest, err)
	}

	// Availability only moves towards reclamation.
	due := time.Now().Add(-time.Minute)
	if _, err = r.SetAvailability(ctx, first.ID, blobs.AvailabilityReclaimed, nil); !errors.Is(err, dberr.ErrInvalidStateTransition) {
		t.Fatalf("skipped pending: %v", err)
	}
	if _, err = r.SetAvailability(ctx, first.ID, blobs.AvailabilityPending, &due); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pending, err := r.Blobs(ctx, blobs.BlobFilter{DueBy: &now})
	if err != nil || len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("due blobs: %+v %v", pending, err)
	}
	if _, err = r.SetAvailability(ctx, first.ID, blobs.AvailabilityReclaimed, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SetAvailability(ctx, first.ID, blobs.AvailabilityPending, nil); !errors.Is(err, dberr.ErrInvalidStateTransition) {
		t.Fatalf("reclaimed blob revived: %v", err)
	}

	// Staged files keep the blobs their artifacts were made from.
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "optimize", Name: "Optimize", CreatedBy: "alice", Content: daltest.Script})
	if err != nil {
		t.Fatal(err)
	}
	run := runstore.CreateInput{ID: "run", WorkflowID: wf.ID, WorkspaceID: f.Workspace, ItemID: f.Item, EventID: "event", EventType: "manual", Actor: "alice", WorkflowVersion: 1}
	if err = daltest.Run(ctx, s, run); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("e", 64)
	artifact, err := r.AddArtifact(ctx, blobs.Artifact{WorkspaceID: f.Workspace, ItemID: f.Item, InputKey: key, Sources: []model.ArtifactSource{{ItemID: f.Item, BlobID: second.ID}},
		ObjectKey: "artifact", Filename: "small.pdf", ContentType: "application/pdf", Size: 5, Sha256: strings.Repeat("d", 64), CreatedBy: "alice", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if reused, err := r.ReusableArtifact(ctx, f.Item, key); err != nil || reused.ID != artifact.ID || len(reused.Sources) != 1 {
		t.Fatalf("reusable artifact: %+v %v", reused, err)
	}
	if _, err = r.ReusableArtifact(ctx, f.Item, strings.Repeat("f", 64)); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("another input reused the artifact: %v", err)
	}
	if held, err := r.Staged(ctx, second); err != nil || held {
		t.Fatalf("an unstaged artifact holds its source: %v %v", held, err)
	}
	staged, err := r.Stage(ctx, blobs.StagedFile{WorkspaceID: f.Workspace, ItemID: f.Item, ArtifactID: artifact.ID, RunID: run.ID, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if held, err := r.Staged(ctx, second); err != nil || !held {
		t.Fatalf("staged source: %v %v", held, err)
	}
	// A pending staged file keeps its artifact past its expiry.
	later := time.Now().Add(2 * time.Hour)
	if n, err := r.DeleteExpiredArtifacts(ctx, later, 10); err != nil || n != 0 {
		t.Fatalf("deleted a held artifact: %d %v", n, err)
	}
	if err = r.DiscardStaged(ctx, blobs.StagedSelection{RunIDs: []string{run.ID}}); err != nil {
		t.Fatal(err)
	}
	stored, err := r.StagedFile(ctx, staged.ID)
	if err != nil || stored.State != blobs.StagedDiscarded || stored.ArtifactID != artifact.ID || stored.RunID != run.ID {
		t.Fatalf("discarded staged file: %+v %v", stored, err)
	}
	if held, err := r.Staged(ctx, second); err != nil || held {
		t.Fatalf("discarded staged file holds its source: %v %v", held, err)
	}
	if leased, err := r.Leased(ctx, second.ID); err != nil || leased {
		t.Fatalf("lease: %v %v", leased, err)
	}
	if n, err := r.DeleteExpiredArtifacts(ctx, time.Now(), 10); err != nil || n != 0 {
		t.Fatalf("deleted an unexpired artifact: %d %v", n, err)
	}
	if n, err := r.DeleteExpiredArtifacts(ctx, later, 10); err != nil || n != 1 {
		t.Fatalf("expired artifact: %d %v", n, err)
	}
	if _, err = r.StagedFile(ctx, staged.ID); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("the staged file outlived its artifact: %v", err)
	}
}
