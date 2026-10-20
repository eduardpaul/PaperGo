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
