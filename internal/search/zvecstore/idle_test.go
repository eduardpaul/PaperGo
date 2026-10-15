package zvecstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"papergo/internal/search"
)

func maintainAt(s *Store, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maintain(now)
}

func suspendNative(t *testing.T, s *Store) {
	t.Helper()
	deadline := activityEpoch.Add(time.Duration(s.lastNativeUse.Load())).Add(nativeIdleTimeout)
	if err := maintainAt(s, deadline); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	asleep := s.handle == nil && s.vectorHandle == nil && !s.closed && !s.dirty
	s.mu.RUnlock()
	if !asleep {
		t.Fatal("idle native indexes did not unload cleanly")
	}
}

func idleFixture(t *testing.T) (*Store, search.Query) {
	t.Helper()
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "zvec"))
	d := testDocument("a")
	c := search.Chunk{Part: "read", Text: "invoice payment", Page: 2}
	for _, err := range []error{
		s.UpsertDocuments(ctx, []search.Document{d}, 1),
		s.StageContent(ctx, d.ResourceID, d.BlobID, "generation", []search.Chunk{c}),
		s.PublishContent(ctx, d.ResourceID, d.BlobID, "generation"),
		s.SetVectors(ctx, "model", []search.HashVector{{InputHash: c.Hash(), Vector: []float32{1, 0, 0, 0}}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return s, search.Query{Mode: search.ModeSemantic, Model: "model", Vector: []float32{1, 0, 0, 0}, Content: true, Filter: search.Filter{Access: search.Access{Read: []string{"scope"}}}}
}

func TestIdleNativeReopen(t *testing.T) {
	for _, wake := range []string{"keyword", "semantic", "match", "write"} {
		t.Run(wake, func(t *testing.T) {
			s, semantic := idleFixture(t)
			ctx := context.Background()
			original := s.meta
			fresh := s.Fresh()
			deadline := activityEpoch.Add(time.Duration(s.lastNativeUse.Load())).Add(nativeIdleTimeout)
			if err := maintainAt(s, deadline.Add(-time.Nanosecond)); err != nil {
				t.Fatal(err)
			}
			if s.handle == nil || s.vectorHandle == nil {
				t.Fatal("native indexes unloaded before their idle deadline")
			}
			suspendNative(t, s)
			// Status, source export, cached vectors, embedding jobs and
			// filter-only searches must neither reopen nor lose the catalog.
			if stats, err := s.Stats(ctx); err != nil || stats.Documents != 1 || stats.Chunks != 1 {
				t.Fatal(stats, err)
			}
			if stats, err := s.Embeddings(ctx, "model"); err != nil || stats.Vectors != 1 || stats.Pending != 0 {
				t.Fatal(stats, err)
			}
			if todo, _, err := s.GenerationToEmbed(ctx, "model", "generation"); err != nil || len(todo) != 0 {
				t.Fatal(todo, err)
			}
			records := 0
			if err := s.Export(ctx, func(search.Record) error { records++; return nil }); err != nil || records != 3 {
				t.Fatal(records, err)
			}
			if result, err := s.Search(ctx, search.Query{Filter: semantic.Filter}); err != nil || len(result.Hits) != 1 {
				t.Fatal(result, err)
			}
			if s.handle != nil || s.vectorHandle != nil {
				t.Fatal("catalog reads woke native indexes")
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.Search(cancelled, semantic); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled search", err)
			}
			if s.handle != nil || s.vectorHandle != nil {
				t.Fatal("cancelled search woke native indexes")
			}
			if other, err := Open(ctx, s.path, Options{}); err == nil {
				other.Close()
				t.Fatal("idle store released its ownership lease")
			}
			text, _ := search.Parse("invoice")
			switch wake {
			case "keyword":
				result, err := s.Search(ctx, search.Query{Text: text, Filter: semantic.Filter})
				if err != nil || len(result.Hits) != 1 {
					t.Fatal(result, err)
				}
			case "semantic":
				result, err := s.Search(ctx, semantic)
				if err != nil || len(result.Hits) != 1 || result.Hits[0].Page != 2 {
					t.Fatal(result, err)
				}
			case "match":
				result, err := s.Match(ctx, search.MatchQuery{Text: text, CollectionID: "collection"})
				if err != nil || len(result.Head) != 1 || len(result.Published) != 1 {
					t.Fatal(result, err)
				}
			case "write":
				if err := s.UpsertDocuments(ctx, []search.Document{testDocument("b")}, 2); err != nil {
					t.Fatal(err)
				}
			}
			if s.meta.Collection != original.Collection || s.meta.VectorCollection != original.VectorCollection || s.Fresh() != fresh {
				t.Fatal("waking recreated an index or changed the store's startup state")
			}
			if s.handle == nil || s.vectorHandle == nil {
				t.Fatal("wake did not reopen both native indexes")
			}
			// A second cycle catches stale handles and lost vectors after writes.
			suspendNative(t, s)
			if result, err := s.Search(ctx, semantic); err != nil || len(result.Hits) != 1 || result.Hits[0].ResourceID != "a" {
				t.Fatal(result, err)
			}
		})
	}
}

func TestIdleReopenFailureIsRetryable(t *testing.T) {
	s, q := idleFixture(t)
	suspendNative(t, s)
	path := filepath.Join(s.path, s.meta.VectorCollection)
	if err := os.Rename(path, path+".away"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), q); !errors.Is(err, search.ErrUnavailable) {
		t.Fatal("failed reopen answered as a healthy empty result", err)
	}
	if s.handle != nil || s.vectorHandle != nil {
		t.Fatal("failed reopen published partial native handles")
	}
	if err := os.Rename(path+".away", path); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Search(context.Background(), q); err != nil || len(result.Hits) != 1 {
		t.Fatal(result, err)
	}
	suspendNative(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), q); !errors.Is(err, search.ErrUnavailable) {
		t.Fatal("final Close allowed an idle index to reopen", err)
	}
}

func TestIdleMaintenanceWaitsForActiveNativeRead(t *testing.T) {
	s, _ := idleFixture(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		readDone <- s.readNative(context.Background(), func() error {
			close(entered)
			<-finish
			return nil
		})
	}()
	<-entered
	s.lastNativeUse.Store(-int64(nativeIdleTimeout))
	maintenanceStarted := make(chan struct{})
	maintenanceDone := make(chan error, 1)
	go func() {
		now := time.Now()
		close(maintenanceStarted)
		maintenanceDone <- maintainAt(s, now)
	}()
	<-maintenanceStarted
	close(finish)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-maintenanceDone; err != nil {
		t.Fatal(err)
	}
	if s.handle == nil || s.vectorHandle == nil {
		t.Fatal("maintenance unloaded an index used by an in-flight read")
	}
}

func TestConcurrentIdleWakeAndWrites(t *testing.T) {
	s, semantic := idleFixture(t)
	text, _ := search.Parse("invoice")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 8 {
				if result, err := s.Search(context.Background(), semantic); err != nil || len(result.Hits) != 1 {
					t.Error(result, err)
				}
				if result, err := s.Search(context.Background(), search.Query{Text: text, Filter: semantic.Filter}); err != nil || len(result.Hits) != 1 {
					t.Error(result, err)
				}
			}
		})
	}
	wg.Go(func() {
		for range 8 {
			if err := s.UpsertDocuments(context.Background(), []search.Document{testDocument("a")}, 2); err != nil {
				t.Error(err)
			}
		}
	})
	for range 8 {
		if err := maintainAt(s, time.Now().Add(2*nativeIdleTimeout)); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}
