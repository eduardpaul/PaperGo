package main

import (
	"log/slog"
	"strings"
	"testing"

	"papergo/internal/config"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/documenttext"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

// Every operation kind this executable registers has exactly one worker,
// and config.yaml adjusts workers by name.
func TestWorkersCoverEveryKind(t *testing.T) {
	db := daltest.Database(t)
	service := dms.NewService(db.SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.RegisterPortability(store); err != nil {
		t.Fatal(err)
	}
	if err = (&documenttext.Reader{}).Register(service); err != nil {
		t.Fatal(err)
	}
	rt := workers(service, store, slog.Default())
	if err = configure(rt, nil); err != nil {
		t.Fatal(err)
	}
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
