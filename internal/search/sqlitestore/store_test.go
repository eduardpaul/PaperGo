package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"papergo/internal/search"
	"papergo/internal/search/storetest"
)

func TestOpenRejectsUnrelatedDatabaseWithoutChangingItsData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "search.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE app_data (value TEXT); INSERT INTO app_data VALUES ('important')`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(context.Background(), path, Options{}); err == nil {
		s.Close()
		t.Fatal("accepted a non-search database")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err = db.QueryRow(`SELECT value FROM app_data`).Scan(&value); err != nil || value != "important" {
		t.Fatalf("application data changed: %q %v", value, err)
	}
}

func TestFacetsVerifyEverySourceAndPropagateErrors(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "search.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var docs []search.Document
	for i := range 401 {
		docs = append(docs, search.Document{ResourceID: fmt.Sprint(i), Surface: "published", Views: search.ViewPublished, ScopeID: "s", Name: "Match"})
	}
	if err = s.UpsertDocuments(ctx, docs, 1); err != nil {
		t.Fatal(err)
	}
	seen := map[search.Key]bool{}
	q := search.Query{Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}, Limit: 1, Facets: true,
		VerifyFacets: func(_ context.Context, keys []search.Key) (bool, error) {
			if len(keys) > 200 {
				t.Fatalf("unbounded batch: %d", len(keys))
			}
			for _, key := range keys {
				seen[key] = true
			}
			return true, nil
		}}
	res, err := s.Search(ctx, q)
	if err != nil || res.Facets == nil || len(seen) != len(docs) {
		t.Fatalf("verified %d sources: %+v %v", len(seen), res, err)
	}
	q.VerifyFacets = func(context.Context, []search.Key) (bool, error) { return false, nil }
	if res, err = s.Search(ctx, q); err != nil || res.Facets != nil {
		t.Fatalf("unverified facets: %+v %v", res, err)
	}
	failure := errors.New("authorization failed")
	q.VerifyFacets = func(context.Context, []search.Key) (bool, error) { return false, failure }
	if _, err = s.Search(ctx, q); !errors.Is(err, failure) {
		t.Fatalf("verification error: %v", err)
	}
}

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) search.Store {
		s, err := Open(context.Background(), filepath.Join(t.TempDir(), "search.db"), Options{Languages: []string{"english"}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

func TestOpenRecreatesAnotherSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "search.db")
	s, err := Open(ctx, path, Options{})
	if err != nil || !s.Fresh() {
		t.Fatalf("new store: %v %v", s, err)
	}
	if err = s.UpsertDocuments(ctx, []search.Document{{ResourceID: "a", Surface: "published", Name: "kept"}}, 1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(ctx, path, Options{}); err != nil || s.Fresh() {
		t.Fatalf("reopened: %v %v", s, err)
	}
	if _, err = s.DB().ExecContext(ctx, `UPDATE state SET value='0' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(ctx, path, Options{}); err != nil || !s.Fresh() {
		t.Fatalf("other schema: %v %v", s, err)
	}
	defer s.Close()
	if st, err := s.Stats(ctx); err != nil || st.Documents != 0 {
		t.Fatalf("recreated store holds %+v %v", st, err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal(err)
	}
	// Other languages need other stems: the store starts over.
	if err = s.UpsertDocuments(ctx, []search.Document{{ResourceID: "a", Surface: "published", Name: "kept"}}, 1); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s, err = Open(ctx, path, Options{Languages: []string{"german"}}); err != nil || !s.Fresh() {
		t.Fatalf("other languages: %v %v", s, err)
	}
	s.Close()
	if s, err = Open(ctx, path, Options{Languages: []string{"German "}}); err != nil || s.Fresh() {
		t.Fatalf("same languages: %v %v", s, err)
	}
	s.Close()
	if _, err = Open(ctx, path, Options{Languages: []string{"klingon"}}); err == nil {
		t.Fatal("unknown language")
	}
}

// Whatever people type, a parsed query renders to FTS5 syntax SQLite accepts.
func FuzzRenderedQueriesAreValid(f *testing.F) {
	for _, seed := range []string{`a "b c"* OR d -e NOT f g*`, `"`, `***`, `NEAR(a b)`, `a:b ^c`, `"x" "y"*`, `- OR NOT`, `ü😀 ß`} {
		f.Add(seed)
	}
	s, err := Open(context.Background(), filepath.Join(f.TempDir(), "search.db"), Options{Languages: []string{"english", "german"}})
	if err != nil {
		f.Fatal(err)
	}
	defer s.Close()
	f.Fuzz(func(t *testing.T, text string) {
		q, err := search.Parse(text)
		if err != nil {
			return
		}
		if _, err = s.Search(context.Background(), search.Query{Text: q, Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}}); err != nil {
			t.Fatalf("%q rendered as %q: %v", text, render(q, s.stem), err)
		}
		if _, err = s.Match(context.Background(), search.MatchQuery{CollectionID: "c", Text: search.Literal(text)}); err != nil {
			t.Fatalf("literal %q: %v", text, err)
		}
	})
}

// When the most similar inputs all belong to documents the filters leave
// out, semantic search looks again among the inputs of the documents they
// keep, so a narrow filter still finds its matches.
func TestSemanticSearchLooksPastFilteredCandidates(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "search.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	unit := func(i int) []float32 {
		v := make([]float32, 4)
		v[0], v[1] = 1, float32(i)/10_000
		return v
	}
	var docs []search.Document
	var vectors []search.HashVector
	for i := range passageCandidates + 1 {
		id, blob, ws := fmt.Sprintf("d%d", i), fmt.Sprintf("b%d", i), "crowded"
		if i == passageCandidates {
			ws = "narrow"
		}
		docs = append(docs, search.Document{ResourceID: id, Kind: "item", Surface: search.SurfacePublished, Views: search.ViewHead | search.ViewPublished, WorkspaceID: ws, ScopeID: "s", BlobID: blob, Name: id})
		chunk := search.Chunk{Part: "read", Kind: search.KindContent, Text: fmt.Sprintf("text %d", i)}
		chunk.InputHash = chunk.Hash()
		if err = s.StageContent(ctx, id, blob, "g"+id, []search.Chunk{chunk}); err != nil {
			t.Fatal(err)
		}
		v := unit(i)
		if i == passageCandidates {
			v = []float32{0.6, 0.8, 0, 0} // less similar than all the others
		}
		vectors = append(vectors, search.HashVector{InputHash: chunk.InputHash, Vector: v})
	}
	if err = s.UpsertDocuments(ctx, docs, 1); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if err = s.PublishContent(ctx, d.ResourceID, d.BlobID, "g"+d.ResourceID); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetVectors(ctx, "m", vectors); err != nil {
		t.Fatal(err)
	}
	q := search.Query{Mode: search.ModeSemantic, Vector: []float32{1, 0, 0, 0}, Model: "m", Content: true, Limit: 5,
		Filter: search.Filter{Access: search.Access{Draft: []string{"s"}, Read: []string{"s"}}, WorkspaceID: "narrow"}}
	res, err := s.Search(ctx, q)
	if err != nil || len(res.Hits) != 1 || res.Hits[0].ResourceID != fmt.Sprintf("d%d", passageCandidates) {
		t.Fatalf("%+v %v", res.Hits, err)
	}
	q.Filter.WorkspaceID = ""
	if res, err = s.Search(ctx, q); err != nil || res.Total != search.CandidateLimit || res.Hits[0].ResourceID != "d0" {
		t.Fatalf("unfiltered: %d %+v %v", res.Total, res.Hits, err)
	}
}
