package events_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/events"
	"testing"
	"time"
)

func TestDomainEventAppend_SQLite(t *testing.T)   { testDomainEventAppend(t, daltest.SQLiteStore(t)) }
func TestDomainEventAppend_Postgres(t *testing.T) { testDomainEventAppend(t, daltest.PostgresStore(t)) }
func testDomainEventAppend(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	event := events.Event{ID: "first", WorkspaceID: f.Workspace, CollectionID: f.Source, ResourceID: f.Item, Actor: "alice", Type: "item.updated", Data: map[string]any{"exact": json.Number("9007199254740993")}}
	if err := s.Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatal(err)
	}
	event.Data = map[string]any{"changed": true}
	if err := s.Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Events().Find(ctx, event.ID)
	if err != nil || stored.Data["exact"] != json.Number("9007199254740993") {
		t.Fatalf("event replay changed payload: %+v %v", stored, err)
	}
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	event.ID = "rolled-back"
	if err := s.WithTx(tx).Events().Append(ctx, []events.Event{event}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Events().Find(ctx, event.ID); !dberr.IsNotFound(err) {
		t.Fatalf("event escaped caller transaction: %v", err)
	}
	testEventDispatch(t, s, f.Workspace)
}

func testEventDispatch(t *testing.T, s *dal.Store, workspace string) {
	ctx := context.Background()
	if err := s.Events().Append(ctx, []events.Event{{ID: "second", WorkspaceID: workspace, Actor: "alice", Type: "permissions.changed"}}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Events().Undispatched(ctx, 10)
	if err != nil || len(pending) != 2 || pending[0].ID != "first" || pending[1].ID != "second" || pending[1].ResourceID != "" {
		t.Fatalf("undispatched events: %+v %v", pending, err)
	}
	if err := s.Events().AdvanceFanout(ctx, "second", "target"); err != nil {
		t.Fatal(err)
	}
	if err := s.Events().Dispatch(ctx, []string{"first", "second"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Events().Find(ctx, "second")
	if err != nil || stored.FanoutCursor != "target" || stored.DispatchedAt == nil {
		t.Fatalf("dispatch state: %+v %v", stored, err)
	}
	cutoff := time.Now().Add(time.Second)
	for _, want := range []int64{1, 1, 0} {
		if n, err := s.Events().DeleteDispatched(ctx, cutoff, 1); err != nil || n != want {
			t.Fatalf("deleted %d events, want %d: %v", n, want, err)
		}
	}
}
