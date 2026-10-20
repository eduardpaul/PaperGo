package resultcache

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/jsonutil"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal/daltest"
)

func TestResultsAreIsolatedAndFirstWriterWins(t *testing.T) {
	ctx := context.Background()
	s := New(daltest.Database(t).SQL)
	for _, entry := range []struct{ namespace, scope, payload string }{
		{"extract", "installation", `{"total":12.50}`},
		{"extract", "workspace:one", `["private"]`},
		{"other", "installation", `"another action"`},
		{"numbers", "installation", `12345678901234567890.12345678901234567890`},
	} {
		if _, err := s.PutIfAbsent(ctx, entry.namespace, entry.scope, "any-key", json.RawMessage(entry.payload), time.Hour); err != nil {
			t.Fatal(err)
		}
		winner, err := s.PutIfAbsent(ctx, entry.namespace, entry.scope, "any-key", json.RawMessage(`false`), time.Hour)
		if err != nil || !jsonutil.Equal(winner, []byte(entry.payload)) {
			t.Fatalf("winner %s: %v", winner, err)
		}
		got, err := s.Get(ctx, entry.namespace, entry.scope, "any-key")
		if err != nil || !jsonutil.Equal(got, []byte(entry.payload)) {
			t.Fatalf("%s/%s: %s %v", entry.namespace, entry.scope, got, err)
		}
	}
	if got, err := s.Get(ctx, "extract", "workspace:two", "any-key"); err != nil || got != nil {
		t.Fatalf("missing scope: %s %v", got, err)
	}
}

func TestExpiryTouchReplacementAndCollection(t *testing.T) {
	ctx := context.Background()
	db := daltest.Database(t).SQL
	s := New(db)
	start := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	now := start
	s.now = func() time.Time { return now }
	put := func(key, payload string, ttl time.Duration) {
		t.Helper()
		if _, err := s.PutIfAbsent(ctx, "action", "installation", key, json.RawMessage(payload), ttl); err != nil {
			t.Fatal(err)
		}
	}
	put("touched", `1`, 4*time.Hour)
	put("expired", `2`, 4*time.Hour)
	put("long-lived", `3`, 48*time.Hour)
	now = start.Add(2 * time.Hour)
	if got, err := s.Get(ctx, "action", "installation", "touched"); err != nil || string(got) != "1" {
		t.Fatal(string(got), err)
	}
	var expiry time.Time
	if err := db.QueryRow(`SELECT expires_at FROM cached_results WHERE key='touched'`).Scan(&expiry); err != nil || !expiry.Equal(start.Add(6*time.Hour)) {
		t.Fatal(expiry, err)
	}
	// An expired entry is already a miss before collection runs.
	now = start.Add(4 * time.Hour)
	if got, err := s.Get(ctx, "action", "installation", "expired"); err != nil || got != nil {
		t.Fatalf("expired hit: %s %v", got, err)
	}
	put("expired", `4`, time.Hour)
	if got, err := s.Get(ctx, "action", "installation", "expired"); err != nil || string(got) != "4" {
		t.Fatal(string(got), err)
	}
	now = start.Add(6 * time.Hour)
	if count, err := s.PurgeExpired(ctx, now); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if got, err := s.Get(ctx, "action", "installation", "long-lived"); err != nil || string(got) != "3" {
		t.Fatal(string(got), err)
	}
}

func TestConcurrentWritersReturnOneWinner(t *testing.T) {
	ctx := context.Background()
	s := New(daltest.Database(t).SQL)
	const writers = 12
	var wg sync.WaitGroup
	results := make(chan string, writers)
	errors := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			winner, err := s.PutIfAbsent(ctx, "action", "installation", "same", json.RawMessage(fmt.Sprintf(`{"writer":%d}`, i)), time.Hour)
			results <- string(winner)
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	winner := <-results
	for result := range results {
		if result != winner {
			t.Fatalf("different winners: %s, %s", winner, result)
		}
	}
}

func TestCacheRejectsInvalidEntries(t *testing.T) {
	ctx := context.Background()
	s := New(daltest.Database(t).SQL)
	for _, entry := range []struct {
		namespace, scope, key, payload string
		ttl                            time.Duration
	}{
		{"", "installation", "key", `{}`, time.Hour},
		{"action", "", "key", `{}`, time.Hour},
		{"action", "installation", "", `{}`, time.Hour},
		{"action", "installation", "key", `invalid`, time.Hour},
		{"action", "installation", "key", `{}`, 0},
		{"action", "installation", "key", `"` + strings.Repeat("x", MaxPayloadBytes) + `"`, time.Hour},
	} {
		if _, err := s.PutIfAbsent(ctx, entry.namespace, entry.scope, entry.key, json.RawMessage(entry.payload), entry.ttl); err == nil {
			t.Fatal("accepted invalid entry")
		}
	}
}
