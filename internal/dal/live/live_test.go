package live_test

import (
	"context"
	"papergo/internal/dal/daltest"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/audit"
)

func TestLiveChanges_SQLite(t *testing.T)   { testLiveChanges(t, daltest.SQLiteStore(t)) }
func TestLiveChanges_Postgres(t *testing.T) { testLiveChanges(t, daltest.PostgresStore(t)) }
func testLiveChanges(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	if earliest, latest, err := s.LiveChanges().Bounds(ctx); err != nil || earliest != 0 || latest != 0 {
		t.Fatalf("empty bounds: %d %d %v", earliest, latest, err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := s.Audit().Append(ctx, audit.Event{ID: id, Subject: "alice", Action: "role.update", EntityType: "identity.role", EntityID: id, TraceID: "trace"}); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := s.LiveChanges().After(ctx, 0)
	if err != nil || len(changes) != 2 || changes[0].EntityID != "a" || changes[1].ID <= changes[0].ID {
		t.Fatalf("changes: %+v %v", changes, err)
	}
	if rest, err := s.LiveChanges().After(ctx, changes[0].ID); err != nil || len(rest) != 1 || rest[0].EntityID != "b" {
		t.Fatalf("changes after first: %+v %v", rest, err)
	}
	earliest, latest, err := s.LiveChanges().Bounds(ctx)
	if err != nil || earliest != changes[0].ID || latest != changes[1].ID {
		t.Fatalf("bounds: %d %d %v", earliest, latest, err)
	}
	cutoff := time.Now().Add(time.Second)
	if n, err := s.LiveChanges().CountExpired(ctx, cutoff); err != nil || n != 2 {
		t.Fatalf("expired: %d %v", n, err)
	}
	for _, want := range []int64{1, 1, 0} {
		if n, err := s.LiveChanges().DeleteExpired(ctx, cutoff, 1); err != nil || n != want {
			t.Fatalf("deleted %d, want %d: %v", n, want, err)
		}
	}
	// The highest assigned ID survives collecting every change.
	if earliest, after, err := s.LiveChanges().Bounds(ctx); err != nil || earliest != 0 || after != latest {
		t.Fatalf("bounds after collection: %d %d %v", earliest, after, err)
	}
}

func TestLiveCursorCommitAndRollback(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		s := newStore(t)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		appendEvent := func(store *dal.Store, id string) error {
			_, err := store.Audit().Append(ctx, audit.Event{ID: id, Subject: "alice", Action: "role.update", EntityType: "identity.role", EntityID: id, TraceID: "trace"})
			return err
		}
		if err := appendEvent(s, "committed"); err != nil {
			t.Fatal(err)
		}
		_, before, err := s.LiveChanges().Bounds(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := appendEvent(s.WithTx(tx), "rolled-back"); err != nil {
			t.Fatal(err)
		}
		if _, latest, err := s.LiveChanges().Bounds(ctx); err != nil || latest != before {
			t.Fatalf("uncommitted cursor became visible: %d, want %d: %v", latest, before, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		if _, latest, err := s.LiveChanges().Bounds(ctx); err != nil || latest != before {
			t.Fatalf("rollback advanced cursor: %d, want %d: %v", latest, before, err)
		}

		first, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Rollback()
		if err := appendEvent(s.WithTx(first), "first"); err != nil {
			t.Fatal(err)
		}
		second := make(chan error, 1)
		started := make(chan struct{})
		go func() {
			close(started)
			second <- appendEvent(s, "second")
		}()
		<-started
		select {
		case err := <-second:
			t.Fatalf("later notification completed before the first committed: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
		if err := first.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		changes, err := s.LiveChanges().After(ctx, before)
		if err != nil || len(changes) != 2 {
			t.Fatalf("changes: %+v %v", changes, err)
		}
		for i, id := range []string{"first", "second"} {
			if changes[i].EntityID != id || changes[i].ID <= before {
				t.Fatalf("notification order: %+v", changes)
			}
			before = changes[i].ID
		}
	})
}
