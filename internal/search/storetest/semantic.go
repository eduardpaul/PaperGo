package storetest

import (
	"math"
	"slices"
	"testing"
	"time"

	"papergo/internal/ai/aitest"
	"papergo/internal/search"
)

// model is the vector space of the semantic tests: the fake embeddings of
// aitest, whose texts are similar when they share words.
const model = "test:fake:64"

func vectorOf(text string) []float32 {
	v := aitest.Vector(text)
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(sum))
	}
	return v
}

// embedAll gives every published chunk without a vector its vector, as the
// sweep does.
func embedAll(t *testing.T, s search.Store) {
	t.Helper()
	todo, err := s.ChunksToEmbed(ctx, model, 1000)
	if err != nil {
		t.Fatal(err)
	}
	vectors := make([]search.HashVector, len(todo))
	for i, c := range todo {
		vectors[i] = search.HashVector{InputHash: c.InputHash, Vector: vectorOf(c.Input)}
	}
	if err = s.SetVectors(ctx, model, vectors); err != nil {
		t.Fatal(err)
	}
}

func semanticSearch(t *testing.T, s search.Store, mode, text string, f search.Filter) search.Result {
	t.Helper()
	q := search.Query{Mode: mode, Vector: vectorOf(text), Model: model, Filter: f, Content: true, Limit: 100, Facets: true}
	if parsed, err := search.Parse(text); err == nil {
		q.Text = parsed
	}
	res, err := s.Search(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != len(res.Hits) {
		t.Fatalf("%s %q: total %d for %d hits", mode, text, res.Total, len(res.Hits))
	}
	return res
}

func ids(res search.Result) []string {
	out := []string{}
	for _, h := range res.Hits {
		out = append(out, h.ResourceID)
	}
	return out
}

// Semantic search finds file content close in meaning, without every word,
// with the page of the most similar chunk; it reads only what the caller may,
// and never metadata alone.
func semanticFindsContentByMeaning(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "Utility", "b1"), withFile("b", "Recipes", "b2"), doc("c", "invoice payment overdue penalty"))
	stage(t, s, "a", "b1", "g1", chunks("read", "Welcome letter", "the invoice payment is overdue"))
	stage(t, s, "b", "b2", "g2", chunks("read", "bake the bread with flour and water"))
	publish(t, s, "a", "b1", "g1")
	publish(t, s, "b", "b2", "g2")
	embedAll(t, s)
	all := search.Filter{Access: everyone}
	expect(t, "keyword needs every word", find(t, s, "invoice payment overdue penalty", all), "c")
	res := semanticSearch(t, s, search.ModeSemantic, "invoice payment overdue penalty", all)
	expect(t, "semantic: file content only", ids(res), "a")
	if h := res.Hits[0]; h.Page != 2 || h.Text != "the invoice payment is overdue" || !slices.Equal(h.MatchedBy, []string{search.MatchedSemantic}) {
		t.Fatalf("semantic hit: %+v", h)
	}
	if res.Facets == nil || len(res.Facets.Workspace) != 1 || res.Facets.Workspace[0].Count != 1 {
		t.Fatalf("facets of the semantic matches: %+v", res.Facets)
	}
	expect(t, "unrelated text is not similar", ids(semanticSearch(t, s, search.ModeSemantic, "quantum chromodynamics", all)))
	expect(t, "other scopes", ids(semanticSearch(t, s, search.ModeSemantic, "invoice payment overdue", search.Filter{Access: search.Access{Draft: []string{"other"}, Read: []string{"other"}}})))
	expect(t, "other workspaces", ids(semanticSearch(t, s, search.ModeSemantic, "invoice payment overdue", search.Filter{Access: everyone, WorkspaceID: "w2"})))
	q := search.Query{Mode: search.ModeSemantic, Vector: vectorOf("invoice payment overdue"), Model: model, Filter: all, Limit: 10}
	if res, err := s.Search(ctx, q); err != nil || len(res.Hits) != 0 {
		t.Fatalf("semantic search without content access: %+v %v", res.Hits, err)
	}
	q.Model = "other:model:64"
	q.Content = true
	if res, err := s.Search(ctx, q); err != nil || len(res.Hits) != 0 {
		t.Fatalf("vectors of another model: %+v %v", res.Hits, err)
	}
	if _, err := s.Search(ctx, search.Query{Mode: search.ModeHybrid, Filter: all, Content: true}); err == nil {
		t.Fatal("hybrid search without a vector")
	}
}

// Hybrid search fuses keyword and semantic matches by reciprocal rank and
// says how each hit was found; excluded words exclude semantic matches too.
func hybridFusesBothLists(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "Utility", "b1"), doc("k", "late penalty clause"))
	stage(t, s, "a", "b1", "g1", chunks("read", "the invoice payment is overdue"))
	publish(t, s, "a", "b1", "g1")
	embedAll(t, s)
	all := search.Filter{Access: everyone}
	res := semanticSearch(t, s, search.ModeHybrid, "invoice OR payment OR overdue OR penalty", all)
	expect(t, "hybrid", ids(res), "a", "k")
	for _, h := range res.Hits {
		want := []string{search.MatchedKeyword}
		if h.ResourceID == "a" {
			want = append(want, search.MatchedSemantic)
		}
		if !slices.Equal(h.MatchedBy, want) {
			t.Fatalf("%s matched by %v, want %v", h.ResourceID, h.MatchedBy, want)
		}
	}
	if res.Hits[0].ResourceID != "a" {
		t.Fatalf("a match on both sides ranks first: %v", ids(res))
	}
	expect(t, "excluded words", ids(semanticSearch(t, s, search.ModeHybrid, "invoice payment penalty -overdue", all)))
	keyword := semanticSearch(t, s, search.ModeKeyword, "penalty", all)
	if len(keyword.Hits) != 1 || !slices.Equal(keyword.Hits[0].MatchedBy, []string{search.MatchedKeyword}) {
		t.Fatalf("keyword hit: %+v", keyword.Hits)
	}
}

// Vectors are kept per chunk input and model: equal text embeds once, a
// staged generation lists only what is new, and unused vectors are collected
// after a while.
func vectorsAreReusedByInput(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "One", "b1"), withFile("b", "Two", "b2"))
	stage(t, s, "a", "b1", "g1", chunks("read", "shared terms and conditions", "only in a"))
	todo, inputs, err := s.GenerationToEmbed(ctx, model, "g1")
	if err != nil || len(todo) != 2 || inputs != 2 || todo[0].Input != "shared terms and conditions" {
		t.Fatalf("staged: %+v %d %v", todo, inputs, err)
	}
	if pending, _ := s.ChunksToEmbed(ctx, model, 10); len(pending) != 0 {
		t.Fatalf("staged chunks are not swept: %+v", pending)
	}
	vectors := []search.HashVector{}
	for _, c := range todo {
		vectors = append(vectors, search.HashVector{InputHash: c.InputHash, Vector: vectorOf(c.Input)})
	}
	if err = s.SetVectors(ctx, model, vectors); err != nil {
		t.Fatal(err)
	}
	publish(t, s, "a", "b1", "g1")
	stage(t, s, "b", "b2", "g2", chunks("read", "shared terms and conditions", "only in b"))
	if todo, inputs, err = s.GenerationToEmbed(ctx, model, "g2"); err != nil || len(todo) != 1 || inputs != 2 || todo[0].Input != "only in b" {
		t.Fatalf("reuse: %+v %d %v", todo, inputs, err)
	}
	publish(t, s, "b", "b2", "g2")
	st, err := s.Embeddings(ctx, model)
	if err != nil || st.Vectors != 2 || st.Pending != 1 || st.MemoryBytes != 2*64*4 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	embedAll(t, s)
	if st, _ = s.Embeddings(ctx, model); st.Pending != 0 || st.Vectors != 3 {
		t.Fatalf("after the sweep: %+v", st)
	}
	expect(t, "both items share the vector", ids(semanticSearch(t, s, search.ModeSemantic, "shared terms and conditions", search.Filter{Access: everyone})), "a", "b")
	// b's file goes away: its own input is unused, the shared one is not.
	if err = s.DeleteContent(ctx, "b", nil); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	if n, err := s.CollectVectors(ctx, model, now, week); err != nil || n != 0 {
		t.Fatalf("collected at once: %d %v", n, err)
	}
	if n, err := s.CollectVectors(ctx, model, now.Add(week), week); err != nil || n != 1 {
		t.Fatalf("collected after a week: %d %v", n, err)
	}
	expect(t, "the shared vector stays", ids(semanticSearch(t, s, search.ModeSemantic, "shared terms and conditions", search.Filter{Access: everyone})), "a")
	// Another model's vectors are collected the same way.
	if n, err := s.CollectVectors(ctx, "test:other:64", now.Add(2*week), week); err != nil || n != 0 {
		t.Fatalf("marking another model: %d %v", n, err)
	}
	if n, err := s.CollectVectors(ctx, "test:other:64", now.Add(3*week), week); err != nil || n != 2 {
		t.Fatalf("collecting another model: %d %v", n, err)
	}
}
