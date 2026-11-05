package main

import (
	"log/slog"
	"papergo/internal/ai"
	"papergo/internal/aiops"
	"papergo/internal/decision"
	"papergo/internal/documentfiles"
	"papergo/internal/documentpreview"
	"papergo/internal/documentsearch"
	"papergo/internal/documenttext"
	"papergo/internal/scriptrun"
	"slices"
	"strings"
	"testing"

	"papergo/extensions/mcp"
	mcpserver "papergo/extensions/mcp/server"
	"papergo/internal/apps"
	"papergo/internal/comments"
	"papergo/internal/config"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

// Every operation kind this executable registers has exactly one worker,
// whichever features run, and config.yaml adjusts workers by name.
func TestWorkersCoverEveryKind(t *testing.T) {
	for _, search := range []bool{false, true} {
		rt := testRuntime(t, testFeatures(search))
		if err := configure(rt, nil); err != nil {
			t.Fatal(search, err)
		}
		if (rt.Worker("search-sync") != nil) != search {
			t.Fatal("the search workers run only with search", search)
		}
	}
	rt := testRuntime(t, testFeatures(true))
	two := 2
	settings := map[string]config.Worker{"documents": {Concurrency: &two, Window: &config.WorkerWindow{TimeZone: "UTC", From: "22:00", To: "06:00"}}}
	if err := configure(rt, settings); err != nil {
		t.Fatal(err)
	}
	if d := rt.Worker("documents"); d.Concurrency != 2 || d.Window == nil {
		t.Fatalf("documents worker = %+v", d)
	}
	if err := configure(rt, map[string]config.Worker{"ocr": {Concurrency: &two}}); err == nil || !strings.Contains(err.Error(), "no worker is named ocr") {
		t.Fatalf("unknown worker: %v", err)
	}
	bad := map[string]config.Worker{"integrations": {Admission: &config.WorkerAdmission{Policy: string(worker.Threshold)}}}
	if err := configure(rt, bad); err == nil {
		t.Fatal("a threshold policy without a threshold was accepted")
	}
}

// Each AI worker runs only with the model it needs, and the rest of the
// runtime still validates.
func TestAIWorkersFollowTheirModels(t *testing.T) {
	for _, tc := range []struct {
		name      string
		assistant *aiops.Assistant
		want      []string
	}{
		{"chat", &aiops.Assistant{Chat: testChat{}}, []string{"ai"}},
		{"batch", &aiops.Assistant{Batch: testBatch{}}, []string{"ai-batch"}},
		{"decisions", &aiops.Assistant{Decisions: testDecisions{}}, []string{"decisions"}},
		{"all", testAssistant(), []string{"ai", "ai-batch", "decisions"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			on := aiFeature(tc.assistant, func() {})
			if on == nil {
				t.Fatal("AI is off with a model")
			}
			rt := testRuntime(t, features{*on, documentsFeature(&documenttext.Reader{}, &documentfiles.Maker{}), previewsFeature(&documentpreview.Renderer{})})
			if err := configure(rt, nil); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"ai", "ai-batch", "decisions"} {
				if (rt.Worker(name) != nil) != slices.Contains(tc.want, name) {
					t.Errorf("worker %s: running %v, want %v", name, rt.Worker(name) != nil, tc.want)
				}
			}
		})
	}
	if aiFeature(&aiops.Assistant{}, func() {}) != nil {
		t.Fatal("AI is on without a model")
	}
}

// A deployment that leaves every optional feature off has none of their
// routes or workers, and keeps the retention of what AI stored.
func TestOffFeaturesAreAbsent(t *testing.T) {
	db := daltest.Database(t)
	service := dms.NewService(db.SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	list, err := enabled(t.Context(), environment{config: config.Config{DatabasePath: t.TempDir() + "/papergo.db"}, db: db, service: service, store: store, workflows: &workflow.Service{DMS: service}, log: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	defer list.close()
	var names []string
	for _, f := range list {
		names = append(names, f.name)
	}
	if want := []string{"documents", "previews", "apps", "ai-retention"}; !slices.Equal(names, want) {
		t.Fatalf("features = %v, want %v", names, want)
	}
	if err = list.register(service); err != nil {
		t.Fatal(err)
	}
	a := &httpapi.API{DMS: service, Logger: slog.Default(), Modules: list.modules()}
	routes, err := a.Routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []string{"GET /v1/items/{id}/comments", "GET /v1/items/{id}/mentionable", "POST /v1/ai/completions", "POST /v1/ai/decisions", "GET /v1/search", "POST /v1/search/reindex", "POST /v1/mcp"} {
		if slices.Contains(routes, off) {
			t.Errorf("route %s is served with its feature off", off)
		}
	}
	if err = service.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	runs := &scriptrun.Runner{DMS: service}
	if err = runs.Register(service); err != nil {
		t.Fatal(err)
	}
	rt := workers(service, store, list, runs, slog.Default())
	if err = configure(rt, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ai", "ai-batch", "decisions", "search", "search-sync"} {
		if rt.Worker(name) != nil {
			t.Errorf("worker %s runs with its feature off", name)
		}
	}
	if !slices.ContainsFunc(list, func(f feature) bool {
		return slices.ContainsFunc(f.maintenance, func(m worker.Maintenance) bool { return m.Name == "AI records" })
	}) {
		t.Error("AI records stopped expiring without a model")
	}
}

// testRuntime is the worker runtime of a service that registered f.
func testRuntime(t *testing.T, f features) *worker.Runtime {
	t.Helper()
	db := daltest.Database(t)
	service := dms.NewService(db.SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	if err = f.register(service); err != nil {
		t.Fatal(err)
	}
	runs := &scriptrun.Runner{DMS: service}
	if err = runs.Register(service); err != nil {
		t.Fatal(err)
	}
	return workers(service, store, f, runs, slog.Default())
}

// testFeatures are every feature, without engines, with search or without.
func testFeatures(search bool) features {
	registry, _ := mcp.NewRegistry(nil, nil, nil)
	assistant := testAssistant()
	f := features{
		documentsFeature(&documenttext.Reader{}, &documentfiles.Maker{}),
		previewsFeature(&documentpreview.Renderer{}),
		{name: "apps", modules: []httpapi.Module{apps.Module()}},
		{name: "comments", modules: []httpapi.Module{comments.Module()}},
		*aiFeature(assistant, func() {}),
		aiRetentionFeature(assistant, nil),
		mcpFeature(mcpserver.New(mcpserver.Config{}, registry)),
	}
	if search {
		f = append(f, searchFeature(&documentsearch.Search{}))
	}
	return f
}

// testAssistant has every model, none of which is ever called.
func testAssistant() *aiops.Assistant {
	return &aiops.Assistant{Chat: testChat{}, Batch: testBatch{}, Decisions: testDecisions{}}
}

type (
	testChat      struct{ ai.Chat }
	testBatch     struct{ ai.Batcher }
	testDecisions struct{ decision.Model }
)
