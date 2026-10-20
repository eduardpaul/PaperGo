package main

import (
	"log/slog"

	"papergo/internal/aiops"
	"papergo/internal/documentfiles"
	"papergo/internal/documentpreview"
	"papergo/internal/documentsearch"
	"papergo/internal/documenttext"
	"papergo/internal/scriptrun"
	"strings"
	"testing"

	"papergo/internal/config"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

// Every operation kind this executable registers has exactly one worker,
// with search and without, and config.yaml adjusts workers by name.
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

// testFeatures are the feature modules without engines or models, with
// search or without.
func testFeatures(search bool) features {
	f := features{text: &documenttext.Reader{}, preview: &documentpreview.Renderer{}, ai: &aiops.Assistant{}, files: &documentfiles.Maker{}}
	if search {
		f.search = &documentsearch.Search{}
	}
	return f
}
