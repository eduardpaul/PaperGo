package activitystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"papergo/internal/activitystore"
	"papergo/internal/testutil"
)

func TestReferencesRetainExpiredPayloadsAndTheirResponses(t *testing.T) {
	db := testutil.Database(t)
	s, ctx := activitystore.New(db.SQL), context.Background()
	expired := time.Now().Add(-time.Hour)
	for _, key := range []string{"request", "response", "unreferenced"} {
		if err := s.PutImmutable(ctx, activitystore.Record{Namespace: "test", Scope: "queue", Key: key, Payload: json.RawMessage(`{"text":"kept"}`), ExpiresAt: &expired}); err != nil {
			t.Fatal(err)
		}
	}
	request := activitystore.ID("test", "queue", "request")
	response := activitystore.ID("test", "queue", "response")
	if err := s.Reference(ctx, "wait", request); err != nil {
		t.Fatal(err)
	}
	if err := s.Reference(ctx, request, response); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"request", "response"} {
		r, err := s.Get(ctx, "test", "queue", key)
		if err != nil || r == nil {
			t.Fatalf("live %s was lost: %+v %v", key, r, err)
		}
	}
	if r, err := s.Get(ctx, "test", "queue", "unreferenced"); err != nil || r != nil {
		t.Fatalf("expired payload retained: %+v %v", r, err)
	}
	if err := s.Release(ctx, "wait"); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM activity_records`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unconsumed storage: %d %v", count, err)
	}
}

func TestLeaseExpiryAndFencing(t *testing.T) {
	db := testutil.Database(t)
	s, ctx := activitystore.New(db.SQL), context.Background()
	if err := s.Put(ctx, activitystore.Record{Namespace: "test", Scope: "queue", Key: "state", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.Claim(ctx, "test", "queue", "state", "first", time.Hour)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := s.Claim(ctx, "test", "queue", "state", "second", time.Hour); err != nil || ok {
		t.Fatal("live lease stolen", ok, err)
	}
	if _, err := db.SQL.Exec(`UPDATE activity_records SET lease_until=?`, time.Now().Add(-time.Hour).UTC()); err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.Claim(ctx, "test", "queue", "state", "second", time.Hour)
	if err != nil || !ok || first == second {
		t.Fatal(ok, err)
	}
	if err := s.CheckLease(ctx, "test", "queue", "state", "first", first); !errors.Is(err, activitystore.ErrLease) {
		t.Fatalf("stale writer accepted: %v", err)
	}
	if err := s.ReleaseLease(ctx, "test", "queue", "state", "first", first); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckLease(ctx, "test", "queue", "state", "second", second); err != nil {
		t.Fatal("stale release removed current lease", err)
	}
}
