package scriptrun

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"papergo/internal/worker"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/script"
	"papergo/internal/storage"
	"papergo/internal/testutil/searchstore"
)

func TestMain(m *testing.M) { daltest.Main(m) }

// crashing fails the first request after its step committed, as a process
// that dies before the script sees the response.
type crashing struct {
	*Effects
	crashed bool
}

var errCrash = errors.New("the process stopped")

func (c *crashing) Request(ctx context.Context, run script.Run, unsaved []runs.Step, step runs.Step, req script.Request) (script.Response, error) {
	response, err := c.Effects.Request(ctx, run, unsaved, step, req)
	if err == nil && !c.crashed {
		c.crashed = true
		return script.Response{}, errCrash
	}
	return response, err
}

const source = `
import { workflow } from "@papergo/workflow";

export default workflow({
  async run(ctx, input: { list: string }) {
    const item = await ctx.api.v1.resources.byId(input.list).children.post({ kind: "item", name: "Invoice 7" });
    const refused = await ctx.api.v1.resources
      .byId("00000000-0000-4000-8000-000000000000")
      .children.post({ kind: "item", name: "Nowhere" })
      .then(() => 0, (e: any) => e.responseStatusCode);
    const bulk = await ctx.api.v1.resources.byId(input.list).bulkUpdate.post({ name: "Reviewed" });
    ctx.log("bulk update", bulk?.status);
    const done = await ctx.wait(bulk!);
    const after = await ctx.api.v1.resources.byId(item!.id!).get();
    return { refused, status: done.status, updated: done.updated, name: after?.name };
  },
});
`

// fixture is a service with alice's workspace, its API and a workflow whose
// runs scripts execute as.
type fixture struct {
	s           *dms.Service
	api         *httpapi.API
	blobs       storage.Store
	workspaceID string
	workflowID  string
}

func setup(t *testing.T, configure ...func(*httpapi.API)) *fixture {
	t.Helper()
	ctx := context.Background()
	db := daltest.Database(t)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	s.OnOperationsFinished(ReleaseWaits(s, slog.New(slog.NewTextHandler(io.Discard, nil))))
	s.SetSearch(searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"}))
	api := &httpapi.API{DMS: s, Storage: blobs, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, c := range configure {
		c(api)
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{s: s, api: api, blobs: blobs, workspaceID: ws.ID, workflowID: uuid.NewString()}
	daltest.Insert(t, s.DAL, "workflows", daltest.Row{"id": f.workflowID, "workspace_id": ws.ID, "key": "review", "name": "Review", "current_version": 1, "created_by": "alice", "updated_by": "alice"})
	daltest.Insert(t, s.DAL, "workflow_versions", daltest.Row{"id": uuid.NewString(), "workflow_id": f.workflowID, "number": 1, "definition": "{}", "created_by": "alice"})
	return f
}

// run records a new run of source with input, acting as alice.
func (f *fixture) run(t *testing.T, source, input string) script.Run {
	t.Helper()
	id := uuid.NewString()
	if err := daltest.WorkflowRun(context.Background(), f.s.DAL, id, f.workflowID, f.workspaceID, "alice", 1); err != nil {
		t.Fatal(err)
	}
	artifact, err := script.Bundle("workflow.ts", source)
	if err != nil {
		t.Fatal(err)
	}
	return script.Run{ID: id, Author: "alice", Artifact: artifact, Input: []byte(input)}
}

// work runs the operation worker until done reports true.
func (f *fixture) work(t *testing.T, done func() bool) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		_ = worker.Single(f.s, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(ctx)
		close(stopped)
	}()
	defer func() { stop(); <-stopped }()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("the operation worker did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestScriptRunsThroughTheAPIExactlyOnce(t *testing.T) {
	ctx := context.Background()
	f := setup(t)
	s := f.s
	list, err := s.Create(ctx, "alice", f.workspaceID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	run := f.run(t, source, `{"list":"`+list.ID+`"}`)
	runID := run.ID
	effects := &crashing{Effects: &Effects{DMS: s, API: f.api}}
	engine := script.New(effects, 0)
	if _, err = engine.Execute(ctx, run); !errors.Is(err, errCrash) {
		t.Fatalf("the first slice ended with %v", err)
	}
	out, err := engine.Execute(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != script.Suspended || len(out.Logs) != 1 || out.Logs[0].Message != "bulk update pending" {
		steps, _ := s.DAL.Runs().Steps(ctx, runID)
		for _, step := range steps {
			t.Logf("step %d %s %s: %s", step.Seq, step.Kind, step.Name, step.Output)
		}
		t.Fatalf("second slice: %+v", out)
	}
	items := daltest.Row{"parent_id": list.ID, "kind": "item"}
	if n := daltest.Count(t, s.DAL, "resources", items); n != 1 {
		t.Fatalf("the list has %d items after replaying the committed create", n)
	}
	steps, err := s.DAL.Runs().Steps(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 4 || steps[1].Kind != runs.StepRequest || steps[3].Kind != runs.StepWait || steps[3].Status != runs.StepPending || len(steps[3].Operations) != 0 {
		t.Fatalf("steps after suspending: %+v", steps)
	}
	if again, err := engine.Execute(ctx, run); err != nil || again.Status != script.Suspended {
		t.Fatalf("a run waiting for a pending operation resumed: %+v, %v", again, err)
	}

	// The real operation worker finishes the bulk update, and its end
	// releases the step waiting for it.
	f.work(t, func() bool {
		if steps, err = s.DAL.Runs().Steps(ctx, runID); err != nil {
			t.Fatal(err)
		}
		return steps[3].Status == runs.StepCompleted
	})
	pending, err := s.DAL.Operations().List(ctx, operations.Filter{Statuses: []operations.Status{operations.StatusPending}})
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending operations: %v, %v", pending, err)
	}

	out, err = engine.Execute(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != script.Completed || string(out.Output) != `{"refused":404,"status":"succeeded","updated":1,"name":"Reviewed"}` {
		t.Fatalf("last slice: %+v", out)
	}
	if n := daltest.Count(t, s.DAL, "resources", items); n != 1 {
		t.Fatalf("the list has %d items", n)
	}
	if n := daltest.Count(t, s.DAL, "resources", daltest.Row{"name": "Nowhere"}); n != 0 {
		t.Fatal("a refused request wrote")
	}
}
