package zvecstore

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/search"
)

// Run explicitly in native CI; the normal conformance suite uses small vectors.
func TestRealDimensionsSoak(t *testing.T) {
	if os.Getenv("PAPERGO_ZVEC_SOAK") != "1" {
		t.Skip("set PAPERGO_ZVEC_SOAK=1 for the 2000 x 768 native soak")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "zvec")
	s := openTest(t, path)
	const count, dimensions = 2000, 768
	records := make([]search.Record, 0, count*2)
	vectors := make([]search.HashVector, 0, count)
	rng := rand.New(rand.NewPCG(42, 7))
	for i := range count {
		d := testDocument(fmt.Sprintf("%05d", i))
		d.ScopeID = fmt.Sprintf("scope-%05d", i)
		d.Name = "invoice " + d.ResourceID
		d.Fields = []search.Field{{Key: "exact", Type: "integer", Values: []search.FieldValue{{Int: 9007199254740993 + int64(i)}}}}
		c := search.Chunk{Part: "read", Text: "payment for " + d.ResourceID, Page: i%20 + 1}
		c.InputHash = c.Hash()
		stored := search.StoredChunk{ItemID: d.ResourceID, BlobID: d.BlobID, Generation: d.ResourceID, Published: true, Chunk: c}
		records = append(records, search.Record{Document: &d, Stamp: 1}, search.Record{Chunk: &stored})
		v := make([]float32, dimensions)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		vectors = append(vectors, search.HashVector{InputHash: c.InputHash, Vector: v})
	}
	start := time.Now()
	if err := s.Import(ctx, records); err != nil {
		t.Fatal(err)
	}
	if err := s.SetVectors(ctx, "soak:768", vectors); err != nil {
		t.Fatal(err)
	}
	if err := s.write(ctx, s.optimize); err != nil {
		t.Fatal(err)
	}
	load := time.Since(start)
	scopes := make([]string, 20000)
	for i := range scopes {
		scopes[i] = fmt.Sprintf("scope-%05d", i)
	}
	q := search.Query{Mode: search.ModeSemantic, Model: "soak:768", Vector: vectors[731].Vector, MinSimilarity: .999, Content: true, Filter: search.Filter{Access: search.Access{Read: scopes}, Fields: &search.FieldFilter{Key: "exact", Type: "integer", Op: "eq", Values: []search.FieldValue{{Int: 9007199254740993 + 731}}}}}
	check := func() {
		t.Helper()
		result, err := s.Search(ctx, q)
		if err != nil || len(result.Hits) != 1 || result.Hits[0].ResourceID != "00731" || result.Hits[0].Page != 12 {
			t.Fatalf("filtered ANN: %+v %v", result, err)
		}
	}
	// Check native nearest-neighbor retrieval without the exact field
	// restriction too, so a degenerate index cannot pass by selecting one key.
	unfiltered := q
	unfiltered.Filter.Fields = nil
	if result, err := s.Search(ctx, unfiltered); err != nil || len(result.Hits) != 1 || result.Hits[0].ResourceID != "00731" {
		t.Fatalf("unfiltered ANN: %+v %v", result, err)
	}
	samples := make([]int64, 100)
	for i := range samples {
		start := time.Now()
		check()
		samples[i] = time.Since(start).Microseconds()
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				check()
			}
		})
	}
	wg.Go(func() {
		for i := range 10 {
			d := *records[i*2].Document
			d.Name = "renamed invoice"
			if err := s.UpsertDocuments(ctx, []search.Document{d}, 2); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	s = openTest(t, path)
	reopen := time.Since(start)
	check()
	stats, err := s.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embedding, err := s.Embeddings(ctx, "soak:768")
	if err != nil || embedding.Vectors != count || embedding.Pending != 0 {
		t.Fatal(embedding, err)
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	rss := ""
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				rss = strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:"))
			}
		}
	}
	report := map[string]any{"documents": count, "dimensions": dimensions, "readable_scopes": len(scopes), "query_count": len(samples), "query_p50_us": samples[50], "query_p95_us": samples[95], "load_ms": load.Milliseconds(), "reopen_ms": reopen.Milliseconds(), "disk_bytes": stats.Bytes, "cached_vector_bytes": embedding.MemoryBytes, "process_rss": rss}
	raw, _ := json.Marshal(report)
	t.Log(string(raw))
}
