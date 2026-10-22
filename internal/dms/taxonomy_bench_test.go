package dms

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/model"
)

// taxonomyBench is a collection of PAPERGO_BENCH_ITEMS items (default 2000)
// tagged from two 3-level taxonomies of 10×10×10 terms: the keywords set and
// a managed set. Each item holds two leaf keywords of its own, two leaves in
// places (multiple, apply_parents) and one in region (plain).
type taxonomyBench struct {
	s                         *Service
	workspace, list           string
	keywordRoot, keywordLeaf  string
	placeRoot, placeLeaf      string
	keywordRootFamily, leafID []string
}

func newTaxonomyBench(b *testing.B) *taxonomyBench {
	n := 2000
	if v, err := strconv.Atoi(os.Getenv("PAPERGO_BENCH_ITEMS")); err == nil && v > 0 {
		n = v
	}
	ctx := context.Background()
	s := NewService(daltest.Database(b).SQL)
	w, err := s.Create(ctx, "alice", "", CreateResource{Kind: "workspace", Name: "Benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		b.Fatal(err)
	}
	group, err := s.CreateTermGroup(ctx, "alice", TermGroupInput{Name: "Bench"})
	if err != nil {
		b.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", TermSetInput{GroupID: group.ID, Key: "places", Name: "Places"})
	if err != nil {
		b.Fatal(err)
	}
	tree := func(setID, prefix string) (roots []string, leaves []string) {
		for i := range 10 {
			root, err := s.CreateTerm(ctx, "alice", setID, TermInput{Name: fmt.Sprintf("%s%d", prefix, i)})
			if err != nil {
				b.Fatal(err)
			}
			roots = append(roots, root.ID)
			for j := range 10 {
				mid, err := s.CreateTerm(ctx, "alice", setID, TermInput{Name: fmt.Sprintf("%s%d.%d", prefix, i, j), ParentID: &root.ID})
				if err != nil {
					b.Fatal(err)
				}
				for k := range 10 {
					leaf, err := s.CreateTerm(ctx, "alice", setID, TermInput{Name: fmt.Sprintf("%s%d.%d.%d", prefix, i, j, k), ParentID: &mid.ID})
					if err != nil {
						b.Fatal(err)
					}
					leaves = append(leaves, leaf.ID)
				}
			}
		}
		return roots, leaves
	}
	keywordRoots, keywordLeaves := tree(taxonomy.KeywordsSetID, "k")
	placeRoots, placeLeaves := tree(set.ID, "p")
	// Decoded at run time so the benchmark builds where the option is unknown.
	var places model.FieldOptions
	if err = json.Unmarshal([]byte(`{"term_set_id":"`+set.ID+`","multiple":true,"apply_parents":true}`), &places); err != nil {
		b.Fatal(err)
	}
	for _, f := range []CreateField{
		{Key: "places", Label: "Places", Type: "term", Indexed: true, Options: places},
		{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}},
	} {
		if _, err = s.CreateField(ctx, "alice", l.ID, f); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < n; i += MaxBulkOperations {
		ops := []BulkOperation{}
		for j := i; j < i+MaxBulkOperations && j < n; j++ {
			ops = append(ops, BulkOperation{Action: "create", Create: &BulkCreate{
				Name:     fmt.Sprintf("Item %06d", j),
				Keywords: []string{keywordLeaves[j%1000], keywordLeaves[(j*7+3)%1000]},
				Values: map[string]any{
					"places": []any{placeLeaves[j%1000], placeLeaves[(j*13+5)%1000]},
					"region": placeLeaves[(j*3)%1000],
				},
			}})
		}
		if _, err = s.Bulk(ctx, "alice", l.ID, BulkRequest{Operations: ops}); err != nil {
			b.Fatal(err)
		}
	}
	family, err := s.TermFamily(ctx, keywordRoots[0])
	if err != nil {
		b.Fatal(err)
	}
	return &taxonomyBench{s: s, workspace: w.ID, list: l.ID, keywordRoot: keywordRoots[0], keywordLeaf: keywordLeaves[0],
		placeRoot: placeRoots[0], placeLeaf: placeLeaves[0], keywordRootFamily: family, leafID: []string{keywordLeaves[0]}}
}

// BenchmarkTaxonomyReads times the reads parent terms change: keyword and
// term filters at a root (a tenth of the items) and at a leaf, browse by
// keyword, keyword vocabularies and the holders a term event resyncs.
func BenchmarkTaxonomyReads(b *testing.B) {
	x := newTaxonomyBench(b)
	ctx := context.Background()
	filter := func(field, op, value string) QueryRequest {
		raw, _ := json.Marshal(value)
		return QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: field, Op: op, Value: raw}}, IncludeTotal: true}
	}
	queries := []struct {
		name string
		in   QueryRequest
	}{
		{"keywords-eq/root", filter("$keywords", "eq", x.keywordRoot)},
		{"keywords-eq/leaf", filter("$keywords", "eq", x.keywordLeaf)},
		{"places-eq/root", filter("places", "eq", x.placeRoot)},
		{"places-eq/leaf", filter("places", "eq", x.placeLeaf)},
		{"region-eq/leaf", filter("region", "eq", x.placeLeaf)},
		{"region-under/root", filter("region", "under", x.placeRoot)},
	}
	for _, q := range queries {
		b.Run("query/"+q.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := x.s.Query(ctx, "alice", x.list, q.in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	for name, keyword := range map[string]string{"root": x.keywordRoot, "leaf": x.keywordLeaf} {
		b.Run("browse-keyword/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := x.s.Browse(ctx, "alice", Browse{ParentID: x.list, Keyword: keyword}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	b.Run("workspace-keywords", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := x.s.WorkspaceKeywords(ctx, "alice", x.workspace, WorkspaceKeywordsQuery{Limit: 100}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("popular-keywords", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := x.s.PopularKeywords(ctx, "alice", 20); err != nil {
				b.Fatal(err)
			}
		}
	})
	for name, ids := range map[string][]string{"root-family": x.keywordRootFamily, "leaf": x.leafID} {
		b.Run("term-holders/"+name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := x.s.EntriesWithTerms(ctx, x.workspace, "", nil, ids, "", 500); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
