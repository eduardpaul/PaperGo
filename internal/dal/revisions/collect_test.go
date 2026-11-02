package revisions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/events"
	"papergo/internal/dal/revisions"
)

func TestCollectKeepsQueuedRevisions_SQLite(t *testing.T) {
	testCollectKeepsQueuedRevisions(t, daltest.SQLiteStore(t))
}
func TestCollectKeepsQueuedRevisions_Postgres(t *testing.T) {
	testCollectKeepsQueuedRevisions(t, daltest.PostgresStore(t))
}

// An undispatched event keeps the revision it names and the one before it,
// which term-change triggers compare; dispatching it releases both.
func testCollectKeepsQueuedRevisions(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	revs := make([]revisions.Revision, 4)
	for i := range revs {
		rev, err := daltest.Revise(ctx, s, revisions.Revision{ItemID: f.Item, SchemaRevisionID: f.Schema, Name: "Item", CreatedBy: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		revs[i] = rev
	}
	event := events.Event{ID: "queued", Type: "item.updated", WorkspaceID: f.Workspace, ResourceID: f.Item, Actor: "alice", Data: map[string]any{"revision_id": revs[2].ID}}
	if err := s.Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatal(err)
	}
	kept := func(want ...bool) {
		t.Helper()
		if err := s.Revisions().Collect(ctx, f.Item); err != nil {
			t.Fatal(err)
		}
		for i, rev := range revs {
			_, err := s.Revisions().Revision(ctx, rev.ID)
			if found := err == nil; found != want[i] || err != nil && !errors.Is(err, dberr.ErrNotFound) {
				t.Fatalf("revision %d kept = %v, want %v (%v)", i+1, found, want[i], err)
			}
		}
	}
	// The head is the last revision.
	kept(false, true, true, true)
	if err := s.Events().Dispatch(ctx, []string{event.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	kept(false, false, false, true)
}
