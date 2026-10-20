package resultcache_test

import (
	"context"
	"encoding/json"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/resultcache"
	"papergo/internal/jsonutil"
	"sync"
	"testing"
	"time"
)

func TestResultCache_SQLite(t *testing.T)   { testResultCache(t, daltest.SQLiteStore(t)) }
func TestResultCache_Postgres(t *testing.T) { testResultCache(t, daltest.PostgresStore(t)) }
func testResultCache(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	cache := resultcache.New(s.SQLDB())
	payload := json.RawMessage(`{"exact":12345678901234567890.12345678901234567890,"integer":9007199254740993}`)
	if _, err := cache.PutIfAbsent(ctx, "results", "workspace", "key", payload, time.Hour); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			winner, err := cache.PutIfAbsent(ctx, "results", "workspace", "key", json.RawMessage(`false`), time.Hour)
			if err == nil && !jsonutil.Equal(winner, payload) {
				t.Errorf("winner changed: %s", winner)
			}
			errs[i] = err
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := cache.Get(ctx, "results", "workspace", "key")
	if err != nil || !jsonutil.Equal(got, payload) {
		t.Fatalf("cache precision: %s %v", got, err)
	}
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := resultcache.NewTx(tx).PutIfAbsent(ctx, "results", "workspace", "rolled-back", payload, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := cache.Get(ctx, "results", "workspace", "rolled-back"); err != nil || got != nil {
		t.Fatalf("cache escaped transaction: %s %v", got, err)
	}
	if got, err := cache.Get(ctx, "results", "another-workspace", "key"); err != nil || got != nil {
		t.Fatalf("cache crossed scope: %s %v", got, err)
	}
	if n, err := cache.PurgeExpired(ctx, time.Now().UTC().Add(2*time.Hour)); err != nil || n != 1 {
		t.Fatalf("expiry collection: %d %v", n, err)
	}
}
