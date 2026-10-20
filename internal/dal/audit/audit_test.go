package audit_test

import (
	"context"
	"papergo/internal/dal/daltest"
	"testing"

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
