package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/search"
	"papergo/internal/search/storetest"
)

func TestMain(m *testing.M) { daltest.Main(m) }

func open(t *testing.T, db *sql.DB, languages ...string) *Store {
	t.Helper()
	s, err := Open(context.Background(), db, Options{Languages: languages})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) search.Store {
		return open(t, daltest.PostgresDB(t), "english")
	})
}

func TestOpenRecreatesAnotherSchema(t *testing.T) {
	ctx := context.Background()
	db := daltest.PostgresDB(t)
	s := open(t, db)
	if !s.Fresh() {
		t.Fatal("a new store is fresh")
	}
	if err := s.UpsertDocuments(ctx, []search.Document{{ResourceID: "a", Surface: "published", Name: "kept"}}, 1); err != nil {
		t.Fatal(err)
	}
	if s = open(t, db); s.Fresh() {
		t.Fatal("reopened store is fresh")
	}
	if st, err := s.Stats(ctx); err != nil || st.Documents != 1 || st.Bytes == 0 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE papergo_search.state SET value='0' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if s = open(t, db); !s.Fresh() {
		t.Fatal("another schema version is not recreated")
	}
	if st, err := s.Stats(ctx); err != nil || st.Documents != 0 {
		t.Fatalf("recreated store holds %+v %v", st, err)
	}
	if s = open(t, db, "german"); !s.Fresh() {
		t.Fatal("other languages need other stems")
	}
	if s = open(t, db, "German "); s.Fresh() {
		t.Fatal("same languages")
	}
	if _, err := Open(ctx, db, Options{Languages: []string{"klingon"}}); err == nil {
		t.Fatal("unknown language")
	}
}

func TestOpenRejectsAnUnrelatedSchema(t *testing.T) {
	ctx := context.Background()
	db := daltest.PostgresDB(t)
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA papergo_search; CREATE TABLE papergo_search.app_data (value text); INSERT INTO papergo_search.app_data VALUES ('important')`); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, db, Options{}); err == nil {
		t.Fatal("opened a schema holding other tables")
	}
	var value string
	if err := db.QueryRowContext(ctx, `SELECT value FROM papergo_search.app_data`).Scan(&value); err != nil || value != "important" {
		t.Fatalf("application data changed: %q %v", value, err)
	}
}

// The store's tables live in their own schema and leave the application's
// search_path alone.
func TestTablesLiveInTheirOwnSchema(t *testing.T) {
	ctx := context.Background()
	db := daltest.PostgresDB(t)
	open(t, db)
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname='papergo_search'`).Scan(&tables); err != nil || tables != 7 {
		t.Fatalf("tables %d %v", tables, err)
	}
	var path string
	if err := db.QueryRowContext(ctx, `SHOW search_path`).Scan(&path); err != nil || strings.Contains(path, "papergo_search") {
		t.Fatalf("search_path %q %v", path, err)
	}
}

// Instances of the store in several processes share the database: they open
// it together once, their writes do not collide, and each one's in-memory
// vectors find what the others wrote.
func TestStoresShareTheDatabase(t *testing.T) {
	ctx := context.Background()
	db := daltest.PostgresDB(t)
	stores := make([]*Store, 4)
	var wg sync.WaitGroup
	errs := make([]error, len(stores))
	for i := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stores[i], errs[i] = Open(ctx, db, Options{Languages: []string{"english"}})
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	for i, s := range stores {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, blob, generation := fmt.Sprint("d", i), fmt.Sprint("b", i), fmt.Sprint("g", i)
			errs[i] = errors.Join(
				s.UpsertDocuments(ctx, []search.Document{{ResourceID: id, Kind: "item", Surface: search.SurfacePublished, Views: search.ViewPublished, ScopeID: "s", BlobID: blob, Name: "Shared " + id}}, 1),
				s.StageContent(ctx, id, blob, generation, []search.Chunk{{Part: "read", Kind: search.KindContent, Text: "text of " + id}}),
				s.PublishContent(ctx, id, blob, generation))
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	q := search.Query{Mode: search.ModeSemantic, Vector: []float32{1, 0}, Model: "m", Content: true, Limit: 10, Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}}
	if res, err := stores[0].Search(ctx, q); err != nil || len(res.Hits) != 0 {
		t.Fatalf("before vectors: %+v %v", res.Hits, err)
	}
	for i, s := range stores[1:] {
		todo, err := s.ChunksToEmbed(ctx, "m", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range todo {
			if c.Input == fmt.Sprint("text of d", i+1) {
				if err = s.SetVectors(ctx, "m", []search.HashVector{{InputHash: c.InputHash, Vector: []float32{1, 0}}}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	res, err := stores[0].Search(ctx, q)
	if err != nil || res.Total != 3 {
		t.Fatalf("vectors other stores wrote: %+v %v", res.Hits, err)
	}
}

// Semantic search looks past the most similar inputs when the filters leave
// out their documents.
func TestSemanticSearchLooksPastFilteredCandidates(t *testing.T) {
	ctx := context.Background()
	s := open(t, daltest.PostgresDB(t))
	var docs []search.Document
	var vectors []search.HashVector
	for i := range search.PassageCandidates + 1 {
		id, blob, ws := fmt.Sprintf("d%d", i), fmt.Sprintf("b%d", i), "crowded"
		v := []float32{1, float32(i) / 10_000, 0, 0}
		if i == search.PassageCandidates {
			ws, v = "narrow", []float32{0.6, 0.8, 0, 0}
		}
		docs = append(docs, search.Document{ResourceID: id, Kind: "item", Surface: search.SurfacePublished, Views: search.ViewHead | search.ViewPublished, WorkspaceID: ws, ScopeID: "s", BlobID: blob, Name: id})
		chunk := search.Chunk{Part: "read", Kind: search.KindContent, Text: fmt.Sprintf("text %d", i)}
		chunk.InputHash = chunk.Hash()
		if err := s.StageContent(ctx, id, blob, "g"+id, []search.Chunk{chunk}); err != nil {
			t.Fatal(err)
		}
		vectors = append(vectors, search.HashVector{InputHash: chunk.InputHash, Vector: v})
	}
	if err := s.UpsertDocuments(ctx, docs, 1); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if err := s.PublishContent(ctx, d.ResourceID, d.BlobID, "g"+d.ResourceID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetVectors(ctx, "m", vectors); err != nil {
		t.Fatal(err)
	}
	q := search.Query{Mode: search.ModeSemantic, Vector: []float32{1, 0, 0, 0}, Model: "m", Content: true, Limit: 5,
		Filter: search.Filter{Access: search.Access{Draft: []string{"s"}, Read: []string{"s"}}, WorkspaceID: "narrow"}}
	res, err := s.Search(ctx, q)
	if err != nil || len(res.Hits) != 1 || res.Hits[0].ResourceID != fmt.Sprintf("d%d", search.PassageCandidates) {
		t.Fatalf("%+v %v", res.Hits, err)
	}
	q.Filter.WorkspaceID = ""
	if res, err = s.Search(ctx, q); err != nil || res.Total != search.CandidateLimit || res.Hits[0].ResourceID != "d0" {
		t.Fatalf("unfiltered: %d %+v %v", res.Total, res.Hits, err)
	}
}

// Text PostgreSQL refuses (NUL characters, invalid UTF-8) is stored
// without them.
func TestTextIsStorable(t *testing.T) {
	ctx := context.Background()
	s := open(t, daltest.PostgresDB(t))
	d := search.Document{ResourceID: "a", Kind: "item", Surface: search.SurfacePublished, Views: search.ViewPublished, ScopeID: "s", BlobID: "b", Name: "Nul\x00name \xff"}
	if err := s.UpsertDocuments(ctx, []search.Document{d}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.StageContent(ctx, "a", "b", "g", []search.Chunk{{Part: "read", Text: "scanned\x00 text"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.PublishContent(ctx, "a", "b", "g"); err != nil {
		t.Fatal(err)
	}
	q, _ := search.Parse("scanned")
	if res, err := s.Search(ctx, search.Query{Text: q, Content: true, Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}}); err != nil || res.Total != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

// A long text is indexed within the bounds of a tsvector.
func TestLongTextStaysWithinTsvectorBounds(t *testing.T) {
	ctx := context.Background()
	s := open(t, daltest.PostgresDB(t), "english")
	var b strings.Builder
	for i := range 200_000 {
		fmt.Fprintf(&b, "word%d ", i)
	}
	d := search.Document{ResourceID: "a", Kind: "item", Surface: search.SurfacePublished, Views: search.ViewPublished, ScopeID: "s", Name: "Long", Body: b.String()}
	if err := s.UpsertDocuments(ctx, []search.Document{d}, 1); err != nil {
		t.Fatal(err)
	}
	q, _ := search.Parse("word1")
	if res, err := s.Search(ctx, search.Query{Text: q, Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}}); err != nil || res.Total != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

// Records copy between the stores, both ways, without any change.
func TestCopyBetweenStores(t *testing.T) {
	ctx := context.Background()
	source, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	d := search.Document{ResourceID: "copy", Kind: "item", Surface: search.SurfacePublished, Views: search.ViewHead | search.ViewPublished, ScopeID: "s", WorkspaceID: "w", CollectionID: "c",
		Name: "invoice copy", BlobID: "blob", TermIDs: []string{"t1", "t2"}, KeywordIDs: []string{"k"}, CreatedAt: time.Unix(0, 1).UTC(), ModifiedAt: time.Unix(0, 2).UTC()}
	d.Fields = []search.Field{{Key: "large", Type: "integer", Values: []search.FieldValue{{Int: 9007199254740993}}}, {Key: "precise", Type: "number", Values: []search.FieldValue{{Text: "exact-96-bit-ordering-key"}}}}
	if err = source.UpsertDocuments(ctx, []search.Document{d}, 42); err != nil {
		t.Fatal(err)
	}
	chunks := make([]search.Chunk, 205)
	for i := range chunks {
		chunks[i] = search.Chunk{Part: "read", Ordinal: i, Kind: "content", Page: i + 1, Text: "invoice payment " + strings.Repeat("a", i%7+1)}
	}
	if err = source.StageContent(ctx, d.ResourceID, d.BlobID, "published", chunks); err != nil {
		t.Fatal(err)
	}
	if err = source.PublishContent(ctx, d.ResourceID, d.BlobID, "published"); err != nil {
		t.Fatal(err)
	}
	todo, _, err := source.GenerationToEmbed(ctx, "model", "published")
	if err != nil {
		t.Fatal(err)
	}
	vs := []search.HashVector{}
	for _, c := range todo {
		vs = append(vs, search.HashVector{InputHash: c.InputHash, Vector: []float32{1, 0, 0, 0}})
	}
	if err = source.SetVectors(ctx, "model", vs); err != nil {
		t.Fatal(err)
	}
	pg := open(t, daltest.PostgresDB(t), "english")
	if err = search.Copy(ctx, source, pg); err != nil {
		t.Fatal(err)
	}
	if err = search.Copy(ctx, source, pg); err != nil {
		t.Fatal("idempotent import", err)
	}
	back, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "copy.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	if err = search.Copy(ctx, pg, back); err != nil {
		t.Fatal(err)
	}
	records := func(s search.Store) []string {
		t.Helper()
		out := []string{}
		if err := s.Export(ctx, func(r search.Record) error { raw, err := json.Marshal(r); out = append(out, string(raw)); return err }); err != nil {
			t.Fatal(err)
		}
		sort.Strings(out)
		return out
	}
	if a, b := records(source), records(pg); !reflect.DeepEqual(a, b) {
		t.Fatalf("postgres records differ: %d -> %d", len(a), len(b))
	}
	if a, b := records(source), records(back); !reflect.DeepEqual(a, b) {
		t.Fatalf("records copied back differ: %d -> %d", len(a), len(b))
	}
	if hit := find(t, pg, "payment"); hit != "copy" {
		t.Fatalf("copied content: %q", hit)
	}
}

func find(t *testing.T, s search.Store, text string) string {
	t.Helper()
	q, err := search.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(context.Background(), search.Query{Text: q, Content: true, Limit: 1, Filter: search.Filter{Access: search.Access{Read: []string{"s"}}}})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("%q: %+v %v", text, res, err)
	}
	return res.Hits[0].ResourceID
}
