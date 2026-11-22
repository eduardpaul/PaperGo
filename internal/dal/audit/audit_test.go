package audit_test

import (
	"context"
	"fmt"
	"papergo/internal/dal/daltest"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/audit"
)

func TestAuditPages_SQLite(t *testing.T)   { testAuditPages(t, daltest.SQLiteStore(t)) }
func TestAuditPages_Postgres(t *testing.T) { testAuditPages(t, daltest.PostgresStore(t)) }
func testAuditPages(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.Audit().Append(ctx, audit.Event{ID: id, Subject: "alice", Action: "user.update", EntityType: "identity.user", EntityID: "u", TraceID: "trace", Properties: []string{"name"}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Audit().List(ctx, audit.Filter{Actor: "alice", Limit: 2})
	if err != nil || len(first) != 2 || first[0].ID != "c" || first[1].ID != "b" || first[0].Properties[0] != "name" || first[0].Details == nil {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last := first[1]
	rest, err := s.Audit().List(ctx, audit.Filter{Actor: "alice", AfterAt: &last.CreatedAt, AfterID: last.ID, Limit: 2})
	if err != nil || len(rest) != 1 || rest[0].ID != "a" {
		t.Fatalf("next page: %+v %v", rest, err)
	}
	if ok, err := s.Audit().Recorded(ctx, "identity.user", "u", "user.update"); err != nil || !ok {
		t.Fatalf("recorded action: %v %v", ok, err)
	}
}

func TestAuditRetention_SQLite(t *testing.T)   { testAuditRetention(t, daltest.SQLiteStore(t)) }
func TestAuditRetention_Postgres(t *testing.T) { testAuditRetention(t, daltest.PostgresStore(t)) }

// Retention deletes events created before the cutoff, oldest first, a page
// at a time, and keeps the rest.
func testAuditRetention(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	now := time.Now().UTC()
	for i, age := range []time.Duration{20, 18, 1} {
		if _, err := s.Audit().Append(ctx, audit.Event{ID: fmt.Sprintf("event-%d", i), CreatedAt: now.Add(-age * 24 * time.Hour), Subject: "alice", Action: "test", EntityType: "test", TraceID: "trace"}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := now.Add(-15 * 24 * time.Hour)
	ids := func() []string {
		events, err := s.Audit().List(ctx, audit.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(events))
		for i, e := range events {
			out[i] = e.ID
		}
		return out
	}
	if n, err := s.Audit().DeleteExpired(ctx, cutoff, 1); err != nil || n != 1 {
		t.Fatalf("first page: %d %v", n, err)
	}
	if got := ids(); len(got) != 2 || got[0] != "event-2" || got[1] != "event-1" {
		t.Fatalf("the oldest goes first: %v", got)
	}
	if n, err := s.Audit().DeleteExpired(ctx, cutoff, 10); err != nil || n != 1 {
		t.Fatalf("second page: %d %v", n, err)
	}
	if got := ids(); len(got) != 1 || got[0] != "event-2" {
		t.Fatalf("recent event deleted: %v", got)
	}
}
