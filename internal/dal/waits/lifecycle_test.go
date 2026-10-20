package waits_test

import (
	"context"
	"errors"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/waits"
	"papergo/internal/jsonutil"
	"testing"
	"time"
)

func TestWaitLifecycle_SQLite(t *testing.T)   { testWaitLifecycle(t, daltest.SQLiteStore) }
func TestWaitLifecycle_Postgres(t *testing.T) { testWaitLifecycle(t, daltest.PostgresStore) }

func testWaitLifecycle(t *testing.T, newStore func(testing.TB) *dal.Store) {
	ctx := context.Background()
	s, in := waitFixture(t, newStore)
	in.Parking = true
	expired := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	in.Deadline = &expired
	row, err := s.Waits().Put(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	filter := waits.Filter{Kind: in.Kind, WorkspaceID: in.WorkspaceID, Statuses: []waits.Status{waits.StatusOpen}, Parking: true, DeadlineBefore: &expired, Limit: 10}
	rows, err := s.Waits().List(ctx, filter)
	if err != nil || len(rows) != 0 {
		t.Fatalf("exclusive deadline boundary: %+v %v", rows, err)
	}
	now := time.Now().UTC()
	filter.DeadlineBefore = &now
	rows, err = s.Waits().List(ctx, filter)
	if err != nil || len(rows) != 1 {
		t.Fatalf("deadline selection: %+v %v", rows, err)
	}
	claim := waits.ClaimInput{Kind: in.Kind, Key: in.Key, Holder: "worker"}
	_, old, err := s.Waits().Claim(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.Waits().Claim(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Waits().UpdateData(ctx, in.Kind, in.Key, "worker", old, []byte(`{"step":2}`)); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("stale claim update: %v", err)
	}
	if err := s.Waits().ReleaseClaim(ctx, in.Kind, in.Key, "worker", old); !errors.Is(err, dberr.ErrNotFound) {
		t.Fatalf("stale claim release: %v", err)
	}
	if err := s.Waits().UpdateData(ctx, in.Kind, in.Key, "worker", token, []byte(`{"step":2}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Waits().UpdateData(ctx, in.Kind, in.Key, in.RunID, "", []byte(`{"step":3}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Waits().Complete(ctx, in.Kind, in.Key, []byte(`{"output":{"done":true}}`)); err != nil {
		t.Fatal(err)
	}
	pending := waits.Filter{Statuses: []waits.Status{waits.StatusCompleted}, Parking: true, Unnotified: true}
	if rows, err := s.Waits().List(ctx, pending); err != nil || len(rows) != 1 {
		t.Fatalf("parking selection: %+v %v", rows, err)
	}
	if err := s.Waits().MarkNotified(ctx, row.ID, now); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Waits().List(ctx, pending); err != nil || len(rows) != 0 {
		t.Fatalf("notified wait selected: %+v %v", rows, err)
	}
	if err := s.Waits().Consume(ctx, in.Kind, in.Key, in.RunID); err != nil {
		t.Fatal(err)
	}
	consumed, err := s.Waits().Find(ctx, in.Kind, in.Key)
	if err != nil || consumed.Parking || !jsonutil.Equal(consumed.Data, []byte(`{"step":3}`)) {
		t.Fatalf("consumption: %+v %v", consumed, err)
	}
	in.Key = "cancelled"
	if _, err = s.Waits().Put(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := s.Waits().CloseRun(ctx, in.RunID); err != nil {
		t.Fatal(err)
	}
	closed, err := s.Waits().Find(ctx, in.Kind, in.Key)
	if err != nil || closed.Status != waits.StatusClosed || closed.Parking {
		t.Fatalf("run cancellation: %+v %v", closed, err)
	}
	if err := s.Waits().DeleteRuns(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Waits().List(ctx, waits.Filter{RunID: in.RunID}); err != nil || len(rows) != 2 {
		t.Fatalf("empty delete removed waits: %+v %v", rows, err)
	}
	if err := s.Waits().DeleteRuns(ctx, []string{in.RunID}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Waits().List(ctx, waits.Filter{RunID: in.RunID}); err != nil || len(rows) != 0 {
		t.Fatalf("run waits retained: %+v %v", rows, err)
	}
}
