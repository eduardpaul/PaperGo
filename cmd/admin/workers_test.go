package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/database"
	"papergo/internal/worker"
)

func TestWorkersCommand(t *testing.T) {
	ctx := context.Background()
	t.Setenv("DATABASE_URL", "")
	path := filepath.Join(t.TempDir(), "papergo.db")
	if err := database.Initialize(ctx, path); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A running API reports its workers' status.
	raw, _ := json.Marshal(worker.Status{Worker: "documents", State: worker.Running, Pending: 3, UpdatedAt: time.Now().UTC()})
	if err = activitystore.New(db.SQL).Put(ctx, activitystore.Record{Namespace: "worker.status", Scope: "installation", Key: "documents", Payload: raw}); err != nil {
		t.Fatal(err)
	}
	out := new(bytes.Buffer)
	if err = run(ctx, []string{"workers", "status", "--database", path}, out); err != nil || !strings.Contains(out.String(), "documents") || !strings.Contains(out.String(), "3") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if err = run(ctx, []string{"workers", "pause", "--database", path, "ocr"}, out); err == nil || !strings.Contains(err.Error(), "no worker named ocr") {
		t.Fatalf("pausing an unknown worker: %v", err)
	}
	for _, step := range []struct {
		command string
		state   worker.State
	}{{"pause", worker.Paused}, {"drain", worker.Draining}, {"resume", worker.Running}} {
		if err = run(ctx, []string{"workers", step.command, "--database", path, "documents"}, out); err != nil {
			t.Fatal(err)
		}
		c, err := worker.ReadControl(ctx, activitystore.New(db.SQL), "documents")
		if err != nil || c.State != step.state {
			t.Fatalf("%s: %+v %v", step.command, c, err)
		}
	}
	if err = run(ctx, []string{"workers", "flush", "--database", path, "documents"}, out); err != nil {
		t.Fatal(err)
	}
	if c, err := worker.ReadControl(ctx, activitystore.New(db.SQL), "documents"); err != nil || c.Flush == "" || c.State != worker.Running {
		t.Fatalf("flush: %+v %v", c, err)
	}
	if err = run(ctx, []string{"workers", "pause"}, out); err == nil {
		t.Fatal("pause without a worker was accepted")
	}
}
