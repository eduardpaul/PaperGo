package searchcontent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/searchcontent"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const token = "0123456789abcdef0123456789abcdef"

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A file's text becomes searchable in steps: chunks staged under a
// generation, enriched, embedded and published. Publishing a generation
// made from a file the item no longer has changes nothing.
func TestContentSteps(t *testing.T) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	index, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"), sqlitestore.Options{Languages: []string{"english"}})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	s.SetSearch(index)
	x := &searchcontent.Indexer{Storage: store, Search: index}
	if err = x.Register(s); err != nil {
		t.Fatal(err)
	}
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxUpload: 1 << 20, Modules: []httpapi.Module{x.Module()}}
	h := a.Handler()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Letters"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Letter"})
	if err != nil {
		t.Fatal(err)
	}
	upload := func(version, body string) {
		r := httptest.NewRequest("PUT", "/v1/items/"+item.ID+"/content", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("If-Match", `"`+version+`"`)
		r.Header.Set("Content-Type", "application/pdf")
		r.Header.Set("X-Filename", "letter.pdf")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 201 && w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	upload("1", "%PDF-letter")
	accepted := func(path, body string) dms.OperationView {
		t.Helper()
		res := call(t, h, "POST", path, body)
		var op dms.OperationView
		if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil {
			t.Fatal(path, res.Code, res.Body.String())
		}
		if err := worker.Drain(ctx, s); err != nil {
			t.Fatal(err)
		}
		done, err := s.Operation(ctx, "alice", op.ID)
		if err != nil || done.Status != operations.StatusSucceeded {
			t.Fatalf("%s: %+v %v", path, done, err)
		}
		return *done
	}
	chunks := "/v1/items/" + item.ID + "/search/chunks"
	if res := call(t, h, "POST", chunks, `{}`); res.Code != 422 {
		t.Fatal("chunked nothing:", res.Code, res.Body.String())
	}
	staged := accepted(chunks, `{"text":"Quarterly invoice from Northwind for printer paper","chunker":"whole"}`)
	var result searchcontent.ChunkResult
	if json.Unmarshal(staged.Result, &result) != nil || result.Generation != staged.ID || result.Chunks != 1 {
		t.Fatalf("%s", staged.Result)
	}
	gen := "/v1/search/generations/" + result.Generation
	// A second part adds to the generation.
	accepted(chunks, `{"text":"Summary: paper supplies","kind":"summary","generation":"`+result.Generation+`"}`)
	res := call(t, h, "GET", gen+"/chunks?limit=10", "")
	var page searchcontent.ChunkPage
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &page) != nil || len(page.Chunks) != 2 || page.More {
		t.Fatal(res.Code, res.Body.String())
	}
	enrich := `{"enrichments":[{"part":"` + page.Chunks[0].Part + `","ordinal":` + itoa(page.Chunks[0].Ordinal) + `,"text":"An invoice for office supplies"}]}`
	if res = call(t, h, "PUT", gen+"/enrichments", enrich); res.Code != 204 {
		t.Fatal(res.Code, res.Body.String())
	}
	var embedded searchcontent.EmbedResult
	if op := accepted(gen+"/embeddings", ""); json.Unmarshal(op.Result, &embedded) != nil || embedded.Reason == "" {
		t.Fatalf("embedding without a model: %s", op.Result)
	}
	var published searchcontent.PublishResult
	if op := accepted(gen+"/publish", ""); json.Unmarshal(op.Result, &published) != nil || !published.Published {
		t.Fatalf("%s", op.Result)
	}
	// Entries are written by syncing them.
	if res = call(t, h, "POST", "/v1/workspaces/"+ws.ID+"/search/sync", `{"resource_ids":["`+item.ID+`"]}`); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	found, err := s.Search(ctx, "alice", dms.SearchRequest{Q: "northwind"})
	if err != nil || len(found.Hits) != 1 {
		t.Fatalf("%+v %v", found, err)
	}
	// A newer file makes the old generation stale.
	stale := accepted(chunks, `{"text":"An older reading","chunker":"whole"}`)
	upload("2", "%PDF-letter again")
	if op := accepted("/v1/search/generations/"+stale.ID+"/publish", ""); json.Unmarshal(op.Result, &published) != nil || published.Published || published.Reason == "" {
		t.Fatalf("%s", op.Result)
	}
	// A chunk operation that added to a generation is not one.
	if res = call(t, h, "GET", "/v1/search/generations/"+item.ID+"/chunks", ""); res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}

	var sync searchcontent.SyncResult
	if res = call(t, h, "POST", "/v1/workspaces/"+ws.ID+"/search/sync", `{"resource_ids":["`+item.ID+`","gone"]}`); res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &sync) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	other, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	if res = call(t, h, "POST", "/v1/workspaces/"+other.ID+"/search/sync", `{"resource_ids":["`+item.ID+`"]}`); res.Code != 422 {
		t.Fatal("synced another workspace's item:", res.Code, res.Body.String())
	}
	if res = call(t, h, "GET", "/v1/workspaces/"+ws.ID+"/search/term-entries?term_id=missing", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"items":[]`) {
		t.Fatal(res.Code, res.Body.String())
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }
