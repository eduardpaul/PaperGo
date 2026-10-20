package activitystore_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/activitystore"
	"papergo/internal/jsonutil"
)

func runActivityStoreSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("ActivityPayloadReplayCASAndRollback", func(t *testing.T) {
		ctx := context.Background()
		db := newStore(t).SQLDB()
		s := activitystore.New(db)
		in := activitystore.Record{Namespace: "test", Scope: "scope", Key: "key", Payload: []byte(`{"id":9007199254740993,"stage":1}`)}
		if err := s.PutImmutable(ctx, in); err != nil {
			t.Fatal(err)
		}
		in.Payload = []byte(`{ "stage":1.0, "id":9007199254740993 }`)
		if err := s.PutImmutable(ctx, in); err != nil {
			t.Fatalf("equivalent replay: %v", err)
		}
		in.Payload = []byte(`{"id":9007199254740992,"stage":1}`)
		if err := s.PutImmutable(ctx, in); !errors.Is(err, activitystore.ErrConflict) {
			t.Fatalf("precision lost: %v", err)
		}
		id := activitystore.ID(in.Namespace, in.Scope, in.Key)
		previous := []byte(`{"stage":1e0,"id":9007199254740993}`)
		next := []byte(`{"stage":2}`)
		if ok, err := s.CompareAndSwap(ctx, id, previous, next); err != nil || !ok {
			t.Fatalf("CAS rejected: %v %v", ok, err)
		}
		if ok, err := s.CompareAndSwap(ctx, id, previous, []byte(`{"stage":3}`)); err != nil || ok {
			t.Fatalf("stale CAS accepted: %v %v", ok, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		in.Payload = []byte(`{"stage":4}`)
		if err := s.WithTx(tx).Put(ctx, in); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, in.Namespace, in.Scope, in.Key)
		if err != nil || got == nil || !jsonutil.Equal(got.Payload, next) {
			t.Fatalf("rollback lost: %+v %v", got, err)
		}
		list, err := s.List(ctx, in.Namespace, in.Scope, "pending", "", 1)
		if err != nil || len(list) != 1 || list[0].ID != id {
			t.Fatalf("list: %+v %v", list, err)
		}
	})
	t.Run("ActivityLeaseFencingAndRetention", func(t *testing.T) {
		ctx := context.Background()
		db := newStore(t).SQLDB()
		s := activitystore.New(db)
		expired := time.Now().UTC().Add(-time.Hour)
		in := activitystore.Record{Namespace: "test", Scope: "scope", Key: "key", Payload: []byte(`{}`), ExpiresAt: &expired}
		if err := s.Put(ctx, in); err != nil {
			t.Fatal(err)
		}
		id := activitystore.ID(in.Namespace, in.Scope, in.Key)
		if err := s.Reference(ctx, "wait", id); err != nil {
			t.Fatal(err)
		}
		if err := s.Reference(ctx, "wait", id); err != nil {
			t.Fatal(err)
		}
		token, ok, err := s.Claim(ctx, in.Namespace, in.Scope, in.Key, "first", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		if _, ok, err := s.Claim(ctx, in.Namespace, in.Scope, in.Key, "second", time.Minute); err != nil || ok {
			t.Fatalf("lease stolen: %v %v", ok, err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE activity_records SET lease_until=$1 WHERE id=$2`, expired, id); err != nil {
			t.Fatal(err)
		}
		next, ok, err := s.Claim(ctx, in.Namespace, in.Scope, in.Key, "second", time.Minute)
		if err != nil || !ok || next == token {
			t.Fatalf("lease not recovered: %v %v", ok, err)
		}
		if err := s.CheckLease(ctx, in.Namespace, in.Scope, in.Key, "first", token); !errors.Is(err, activitystore.ErrLease) {
			t.Fatalf("stale token accepted: %v", err)
		}
		if err := s.ReleaseLease(ctx, in.Namespace, in.Scope, in.Key, "first", token); err != nil {
			t.Fatal(err)
		}
		if err := s.CheckLease(ctx, in.Namespace, in.Scope, in.Key, "second", next); err != nil {
			t.Fatal(err)
		}
		if err := s.Purge(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Get(ctx, in.Namespace, in.Scope, in.Key); err != nil || got == nil {
			t.Fatalf("reference lost: %+v %v", got, err)
		}
		if err := s.Release(ctx, "wait"); err != nil {
			t.Fatal(err)
		}
		if err := s.ReleaseLeases(ctx, "second"); err != nil {
			t.Fatal(err)
		}
		if err := s.Purge(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM activity_records`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("retention left %d records: %v", n, err)
		}
	})
}

func TestActivityStore(t *testing.T) { daltest.Engines(t, runActivityStoreSuite) }
