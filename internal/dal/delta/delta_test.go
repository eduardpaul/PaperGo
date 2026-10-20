package delta_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dal/daltest"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
)

func TestDeltaViews_SQLite(t *testing.T)   { testDeltaViews(t, daltest.SQLiteStore(t)) }
func TestDeltaViews_Postgres(t *testing.T) { testDeltaViews(t, daltest.PostgresStore(t)) }
func testDeltaViews(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	if _, err := s.Directory().CreateUser(ctx, "alice", "Alice", "", true, false); err != nil {
		t.Fatal(err)
	}
	d := s.Delta()
	if _, err := d.View(ctx, "alice", f.Source); !dberr.IsNotFound(err) {
		t.Fatalf("missing view: %v", err)
	}
	view, err := d.CreateView(ctx, "alice", f.Source)
	if err != nil {
		t.Fatal(err)
	}
	if view, err = d.Advance(ctx, view, nil, map[string]string{f.Item: fingerprint('a')}); err != nil || view.Sequence != 1 {
		t.Fatalf("first snapshot: %+v %v", view, err)
	}
	first, err := d.CreateToken(ctx, view, json.RawMessage(`[{"id":"x"}]`), time.Minute, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if view, err = d.Advance(ctx, view, []string{f.Item}, map[string]string{f.Item: fingerprint('b')}); err != nil || view.Sequence != 2 {
		t.Fatalf("second snapshot: %+v %v", view, err)
	}
	for sequence, want := range map[int64]string{1: fingerprint('a'), 2: fingerprint('b')} {
		if states, err := d.States(ctx, view.ID, sequence); err != nil || len(states) != 1 || states[f.Item] != want {
			t.Fatalf("states at %d: %v %v", sequence, states, err)
		}
	}
	stored, err := d.View(ctx, "alice", f.Source)
	if err != nil || stored.Sequence != 2 {
		t.Fatalf("stored view: %+v %v", stored, err)
	}
	if token, err := d.Token(ctx, first.ID); err != nil || token.Sequence != 1 || string(token.Changes) != `[{"id": "x"}]` && string(token.Changes) != `[{"id":"x"}]` {
		t.Fatalf("token: %+v %v", token, err)
	}
	// The first token keeps the ended state; an expired page is cleared.
	if err := d.CollectExpired(ctx, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if token, err := d.Token(ctx, first.ID); err != nil || string(token.Changes) != "[]" {
		t.Fatalf("expired page: %+v %v", token, err)
	}
	if n, err := d.CountStates(ctx); err != nil || n != 2 {
		t.Fatalf("states named by a token: %d %v", n, err)
	}
	if err := d.Renew(ctx, first.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := d.CollectExpired(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if n, err := d.CountStates(ctx); err != nil || n != 0 {
		t.Fatalf("unreachable states: %d %v", n, err)
	}
	if _, err := d.ViewByID(ctx, view.ID); !dberr.IsNotFound(err) {
		t.Fatalf("tokenless view retained: %v", err)
	}
}

func fingerprint(c byte) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
