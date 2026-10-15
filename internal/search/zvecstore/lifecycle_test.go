package zvecstore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"papergo/internal/search"
)

func testDocument(id string) search.Document {
	return search.Document{ResourceID: id, Surface: search.SurfacePublished, Views: search.ViewHead | search.ViewPublished, Kind: "item", ScopeID: "scope", WorkspaceID: "workspace", CollectionID: "collection", Name: "invoice " + id, BlobID: "blob", CreatedAt: time.Now().UTC(), ModifiedAt: time.Now().UTC()}
}
func openTest(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path, Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func TestLazyReadAndExclusiveOwnership(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "zvec")
	s := openTest(t, path)
	q, _ := search.Parse("invoice")
	if _, err := s.Search(ctx, search.Query{Text: q}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stats(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("read created a collection: %v", err)
	}
	if other, err := Open(ctx, path, Options{}); err == nil {
		other.Close()
		t.Fatal("two writers own the same root")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(ctx, search.Query{}); !errors.Is(err, search.ErrUnavailable) {
		t.Fatal("read after close", err)
	}
}

func TestRestartKeepsVectorsAndMetadataUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "zvec")
	s := openTest(t, path)
	d := testDocument("a")
	if err := s.UpsertDocuments(ctx, []search.Document{d}, 1); err != nil {
		t.Fatal(err)
	}
	c := search.Chunk{Part: "read", Kind: "content", Text: "invoice payment", Page: 2}
	if err := s.StageContent(ctx, d.ResourceID, d.BlobID, "generation", []search.Chunk{c}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishContent(ctx, d.ResourceID, d.BlobID, "generation"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVectors(ctx, "model", []search.HashVector{{InputHash: c.Hash(), Vector: []float32{1, 0, 0, 0}}}); err != nil {
		t.Fatal(err)
	}
	d.Name = "Renamed invoice"
	if err := s.UpsertDocuments(ctx, []search.Document{d}, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScopes(ctx, map[string]string{d.ResourceID: "private"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path)
	if s.Fresh() {
		t.Fatal("reopened store is empty")
	}
	q := search.Query{Mode: search.ModeSemantic, Model: "model", Vector: []float32{1, 0, 0, 0}, Content: true, Filter: search.Filter{Access: search.Access{Read: []string{"private"}}}}
	out, err := s.Search(ctx, q)
	if err != nil || len(out.Hits) != 1 || out.Hits[0].Name != d.Name || out.Hits[0].Page != 2 {
		t.Fatalf("reopened: %+v %v", out, err)
	}
	if todo, _, err := s.GenerationToEmbed(ctx, "model", "generation"); err != nil || len(todo) != 0 {
		t.Fatalf("lost cached vector: %v %v", todo, err)
	}
}

func TestPartialNativeBatchRequiresRebuild(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "zvec")
	s := openTest(t, path)
	err := s.write(ctx, func() error {
		return s.mutation(func() error {
			r := newRow("invalid")
			r.text("missing_column", "probe")
			return writeRows(s.handle, []*row{r}, false)
		})
	})
	if err == nil {
		t.Fatal("invalid native write succeeded")
	}
	if _, err = s.Stats(ctx); !errors.Is(err, search.ErrUnavailable) {
		t.Fatal("partial index answered queries", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path)
	if !s.Fresh() {
		t.Fatal("dirty collection was trusted")
	}
}

func TestAcknowledgedWriteSurvivesProcessExit(t *testing.T) {
	if path := os.Getenv("PAPERGO_ZVEC_CRASH_CHILD"); path != "" {
		s, err := Open(context.Background(), path, Options{Languages: []string{"english"}})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.UpsertDocuments(context.Background(), []search.Document{testDocument("durable")}, 1); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // No Close/flush: the acknowledged write is in zvec's WAL.
	}
	path := filepath.Join(t.TempDir(), "zvec")
	cmd := exec.Command(os.Args[0], "-test.run=^TestAcknowledgedWriteSurvivesProcessExit$")
	cmd.Env = append(os.Environ(), "PAPERGO_ZVEC_CRASH_CHILD="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %s %v", out, err)
	}
	s := openTest(t, path)
	keys, err := s.Keys(context.Background(), []string{"durable"})
	if err != nil || len(keys) != 1 {
		t.Fatalf("lost acknowledged write: %v %v", keys, err)
	}
}

func TestConcurrentReadersAndQueuedWriters(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "zvec"))
	ctx := context.Background()
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		wg.Go(func() {
			if err := s.UpsertDocuments(ctx, []search.Document{testDocument(id)}, 1); err != nil {
				t.Error(err)
			}
			if _, err := s.Stats(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if stats, err := s.Stats(ctx); err != nil || stats.Documents != 8 {
		t.Fatal(stats, err)
	}
}

func TestKeywordCandidateLimitIsReported(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "zvec"), Options{KeywordCandidates: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.UpsertDocuments(context.Background(), []search.Document{testDocument("a"), testDocument("b"), testDocument("c")}, 1); err != nil {
		t.Fatal(err)
	}
	q, _ := search.Parse("invoice")
	result, err := s.Search(context.Background(), search.Query{Text: q, Filter: search.Filter{Access: search.Access{Read: []string{"scope"}}}, Facets: true})
	if err != nil || !result.Estimated || result.Total != 2 {
		t.Fatal(result, err)
	}
}

func TestCachedModelActivationAndVectorCollection(t *testing.T) {
	ctx := context.Background()
	s := openTest(t, filepath.Join(t.TempDir(), "zvec"))
	d := testDocument("a")
	c := search.Chunk{Part: "read", Text: "payment"}
	for _, err := range []error{s.UpsertDocuments(ctx, []search.Document{d}, 1), s.StageContent(ctx, "a", "blob", "g", []search.Chunk{c}), s.PublishContent(ctx, "a", "blob", "g")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []string{"one", "two"} {
		if err := s.SetVectors(ctx, model, []search.HashVector{{InputHash: c.Hash(), Vector: []float32{1, 0, 0, 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	q := search.Query{Mode: search.ModeSemantic, Model: "one", Vector: []float32{1, 0, 0, 0}, Content: true, Filter: search.Filter{Access: search.Access{Read: []string{"scope"}}}}
	if out, err := s.Search(ctx, q); err != nil || len(out.Hits) != 1 {
		t.Fatal(out, err)
	}
	// Invalid inputs must not quarantine an otherwise healthy collection.
	if err := s.SetVectors(ctx, "one", []search.HashVector{{InputHash: "invalid", Vector: []float32{1, 0}}}); err == nil {
		t.Fatal("same model changed dimensions")
	}
	if err := s.UpsertDocuments(ctx, []search.Document{{}}, 2); err == nil {
		t.Fatal("invalid document accepted")
	}
	if _, err := s.Stats(ctx); err != nil {
		t.Fatal("validation poisoned store", err)
	}
	now := time.Now().UTC()
	if _, err := s.CollectVectors(ctx, "two", now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CollectVectors(ctx, "two", now.Add(2*time.Hour), time.Hour); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if out, err := s.Search(ctx, q); err != nil || len(out.Hits) != 0 {
		t.Fatal("collected vector still searchable", out, err)
	}
	q.Model = "two"
	if out, err := s.Search(ctx, q); err != nil || len(out.Hits) != 1 {
		t.Fatal("retained model missing", out, err)
	}
}

func TestIncompleteCollectionCreationRebuildsOwnedDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "zvec")
	s := openTest(t, path)
	// The ownership catalog is durable before native schema creation begins.
	if err := s.write(context.Background(), func() error {
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		if err := s.saveCatalog(); err != nil {
			return err
		}
		return s.markDirty()
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path)
	if !s.Fresh() {
		t.Fatal("incomplete creation was trusted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("incomplete owned directory retained", err)
	}
}
