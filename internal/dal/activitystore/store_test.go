package activitystore_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/daltest"
)

func TestReferencesRetainExpiredPayloadsAndTheirResponses(t *testing.T) {
	db := daltest.Database(t)
	s, ctx := activitystore.New(db.SQL), context.Background()
	expired := time.Now().Add(-time.Hour)
	for _, key := range []string{"request", "response", "unreferenced"} {
		if err := s.PutImmutable(ctx, activitystore.Record{Namespace: "test", Scope: "queue", Key: key, Payload: json.RawMessage(`{"text":"kept"}`), ExpiresAt: &expired}); err != nil {
			t.Fatal(err)
		}
	}
	request := activitystore.ID("test", "queue", "request")
	response := activitystore.ID("test", "queue", "response")
	if err := s.Put(ctx, activitystore.Record{Namespace: "test", Scope: "queue", Key: "holder", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	holder := activitystore.RecordOwner(activitystore.ID("test", "queue", "holder"))
	if err := s.Reference(ctx, holder, request); err != nil {
		t.Fatal(err)
	}
	if err := s.Reference(ctx, activitystore.RecordOwner(request), response); err != nil {
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
	if err := s.Release(ctx, holder); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Only the unexpired holder remains.
	if count := daltest.Count(t, db.DAL, "activity_records", nil); count != 1 {
		t.Fatalf("unconsumed storage: %d", count)
	}
}

func TestLeaseExpiryAndFencing(t *testing.T) {
	db := daltest.Database(t)
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
	daltest.Update(t, db.DAL, "activity_records", daltest.Row{"lease_until": time.Now().Add(-time.Hour).UTC()}, nil)
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
