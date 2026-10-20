package scriptrun

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/worker"
	"papergo/internal/workflow"
)

const token = "0123456789abcdef0123456789abcdef"

// runtime is a running PaperGo: the API, the script runner and a worker
// runtime with the dispatcher, schedules and wait reconciliation.
type runtime struct {
	s       *dms.Service
	blobs   storage.Store
	wf      *workflow.Service
	runs    *Runner
	handler http.Handler
	ws      string
	list    string
}

// run starts a runtime with the feature modules features register.
func run(t *testing.T, features ...func(*dms.Service, storage.Store) httpapi.Module) *runtime {
	t.Helper()
	ctx := context.Background()
	db := daltest.Database(t)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := dms.NewService(db.SQL)
	wf := &workflow.Service{DMS: s}
	api := &httpapi.API{DMS: s, Workflows: wf, Storage: blobs, Logger: log, Auth: auth.Development{Token: token, Subject: "alice"}, MaxUpload: 1 << 20}
	for _, feature := range features {
		api.Modules = append(api.Modules, feature(s, blobs))
	}
	runs := &Runner{DMS: s, API: api, Log: log}
	if err = runs.Register(s); err != nil {
		t.Fatal(err)
	}
	s.OnOperationsFinished(ReleaseWaits(s, log))
	if err = api.Validate(); err != nil {
		t.Fatal(err)
	}
	rt := worker.New(s, log)
	rt.Add(worker.Definition{Name: "all", Kinds: s.OperationKinds(), Concurrency: 4})
	rt.Maintain(worker.Maintenance{Name: "dispatch", Every: 50 * time.Millisecond, Run: runs.Dispatch, Wake: runs.Woken()})
	rt.Maintain(worker.Maintenance{Name: "waits", Every: 50 * time.Millisecond, Run: func(ctx context.Context, _ time.Time) error { return ReconcileWaits(ctx, s) }})
	working, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		if err := rt.Run(working); err != nil {
			t.Error(err)
		}
		close(stopped)
	}()
	t.Cleanup(func() { stop(); <-stopped })
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	return &runtime{s: s, blobs: blobs, wf: wf, runs: runs, handler: api.Handler(), ws: ws.ID, list: list.ID}
}

// call makes an API request as alice and decodes its JSON response into out.
func (r *runtime) call(t *testing.T, method, path, body string, want int, out any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
}

// workflow creates a workflow from source, its automatic triggers on.
func (r *runtime) workflow(t *testing.T, name, source string) workflow.Workflow {
	t.Helper()
	w, err := r.wf.Create(t.Context(), "alice", r.ws, workflow.Save{Name: name, Source: source, AutomaticEnabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// start starts w by hand on items with inputs and returns its run.
func (r *runtime) start(t *testing.T, w workflow.Workflow, inputs map[string]any, items ...string) string {
	t.Helper()
	ids, err := r.wf.StartRuns(t.Context(), "alice", w.ID, workflow.Start{ItemIDs: items, Inputs: inputs})
	if err != nil || len(ids) != 1 {
		t.Fatal(ids, err)
	}
	return ids[0]
}

// await waits until run id is in status and returns it as the API shows it.
func (r *runtime) await(t *testing.T, id string, status runstore.Status) workflow.Run {
	t.Helper()
	var run workflow.Run
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		r.call(t, "GET", "/v1/workflow-runs/"+id, "", 200, &run)
		if run.Status == status {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s is %s, want %s: %+v", id, run.Status, status, run)
		}
	}
}

// item creates an item named name in the runtime's list.
func (r *runtime) item(t *testing.T, name string) string {
	t.Helper()
	item, err := r.s.Create(t.Context(), "alice", r.list, dms.CreateResource{Kind: "item", Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

func (r *runtime) children(t *testing.T, parent string) []string {
	t.Helper()
	var page struct {
		Value []struct {
			Name string `json:"name"`
		} `json:"value"`
	}
	r.call(t, "GET", "/v1/resources/"+parent+"/children", "", 200, &page)
	names := make([]string, len(page.Value))
	for i, c := range page.Value {
		names[i] = c.Name
	}
	return names
}

func TestRunsExecuteManualAndAutomaticStarts(t *testing.T) {
	r := run(t)
	manual := r.workflow(t, "File a note", `import { workflow } from "@papergo/workflow";

export default workflow<{ note: string }>({
  triggers: [{ type: "manual" }],
  input_schema: { type: "object", required: ["note"], properties: { note: { type: "string" }, copies: { type: "integer", default: 1 } } },
  output_schema: { type: "object", required: ["id"], properties: { id: { type: "string" } } },
  async run(ctx, input) {
    ctx.log("filing", input.note);
    const item = await ctx.api.v1.resources.byId("`+r.list+`").children.post({ kind: "item", name: input.note });
    return { id: item!.id };
  },
});
`)
	id := r.start(t, manual, map[string]any{"note": "From a script"})
	done := r.await(t, id, runstore.StatusCompleted)
	var output struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(done.Result, &output); err != nil || output.ID == "" {
		t.Fatalf("output: %s %v", done.Result, err)
	}
	if len(done.Steps) != 1 || done.Steps[0].Kind != string(runstore.StepRequest) || done.Inputs["copies"] != float64(1) || len(done.Logs) != 1 || done.Logs[0].Message != "filing From a script" {
		t.Fatalf("run: %+v", done)
	}
	if names := r.children(t, r.list); len(names) != 1 || names[0] != "From a script" {
		t.Fatalf("items: %v", names)
	}

	// An item created in the list starts the automatic workflow on it; the
	// item the manual run created started nothing, as it was created before.
	automatic := r.workflow(t, "Describe", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "item.created", collection_id: "`+r.list+`" }],
  async run(ctx) {
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    return { name: item!.name, event: ctx.run.event.type };
  },
});
`)
	itemID := r.item(t, "Invoice 9")
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	for deadline := time.Now().Add(20 * time.Second); len(page.Value) == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the item started no run")
		}
		r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+automatic.ID, "", 200, &page)
	}
	if len(page.Value) != 1 || page.Value[0].ItemID == nil || *page.Value[0].ItemID != itemID {
		t.Fatalf("automatic runs: %+v", page.Value)
	}
	described := r.await(t, page.Value[0].ID, runstore.StatusCompleted)
	if string(described.Result) != `{"event":"item.created","name":"Invoice 9"}` && string(described.Result) != `{"name":"Invoice 9","event":"item.created"}` {
		t.Fatalf("automatic run output: %s", described.Result)
	}
}

func TestRunsWaitForTasksAndSleeps(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Approve", `import { plain, workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual", collection_id: "`+r.list+`" }],
  async run(ctx) {
    const task = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({ kind: "approval", itemId: ctx.itemId, title: "Check it" });
    const answer = plain((await ctx.wait(task!.operationId!)).result) as { outcome: string; comment?: string };
    await ctx.sleep(20);
    return { outcome: answer.outcome, comment: answer.comment };
  },
});
`)
	itemID := r.item(t, "Contract")
	id := r.start(t, w, nil, itemID)
	waiting := r.await(t, id, runstore.StatusWaiting)
	var tasks struct {
		Value []struct {
			ID          string `json:"id"`
			OperationID string `json:"operation_id"`
			Title       string `json:"title"`
		} `json:"value"`
	}
	r.call(t, "GET", "/v1/items/"+itemID+"/tasks", "", 200, &tasks)
	if len(tasks.Value) != 1 || tasks.Value[0].OperationID == "" || tasks.Value[0].Title != "Check it" {
		t.Fatalf("tasks: %+v", tasks)
	}
	if last := waiting.Steps[len(waiting.Steps)-1]; last.Kind != string(runstore.StepWait) || last.Status != string(runstore.StepPending) {
		t.Fatalf("a run waiting for a task: %+v", waiting.Steps)
	}
	r.call(t, "POST", "/v1/tasks/"+tasks.Value[0].ID+"/responses", `{"outcome":"approved","comment":"fine"}`, 200, nil)
	done := r.await(t, id, runstore.StatusCompleted)
	if string(done.Result) != `{"comment":"fine","outcome":"approved"}` && string(done.Result) != `{"outcome":"approved","comment":"fine"}` {
		t.Fatalf("output: %s", done.Result)
	}

	// Cancelling a run that waits for a task cancels the task.
	id = r.start(t, w, nil, itemID)
	r.await(t, id, runstore.StatusWaiting)
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/cancel", "", 200, nil)
	r.await(t, id, runstore.StatusCancelled)
	var open struct {
		Value []struct {
			Status string `json:"status"`
		} `json:"value"`
	}
	r.call(t, "GET", "/v1/items/"+itemID+"/tasks", "", 200, &open)
	for _, task := range open.Value {
		if task.Status == "open" {
			t.Fatalf("a cancelled run left its task open: %+v", open)
		}
	}

	// A task a run does not wait for outlives it.
	followUp := r.workflow(t, "Follow up", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual", collection_id: "`+r.list+`" }],
  async run(ctx) {
    await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({ itemId: ctx.itemId, title: "Look again" });
  },
});
`)
	r.await(t, r.start(t, followUp, nil, itemID), runstore.StatusCompleted)
	r.call(t, "GET", "/v1/items/"+itemID+"/tasks", "", 200, &open)
	still := 0
	for _, task := range open.Value {
		if task.Status == "open" {
			still++
		}
	}
	if still != 1 {
		t.Fatalf("the follow-up task did not outlive its run: %+v", open)
	}
}

func TestFailedRunsRetryFromTheirLastRequest(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Check", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual", collection_id: "`+r.list+`" }],
  async run(ctx) {
    await ctx.api.v1.resources.byId("`+r.list+`").children.post({ kind: "item", name: "Checked" });
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    if (item!.name !== "Ready") throw new Error("the item is not ready");
    return { checked: true };
  },
});
`)
	itemID := r.item(t, "Draft")
	id := r.start(t, w, nil, itemID)
	failed := r.await(t, id, runstore.StatusFailed)
	if !strings.Contains(failed.Error, "the item is not ready") {
		t.Fatalf("failure: %+v", failed)
	}
	var item struct {
		Version int `json:"version"`
	}
	r.call(t, "GET", "/v1/resources/"+itemID, "", 200, &item)
	name := "Ready"
	if _, err := r.s.Update(t.Context(), "alice", itemID, item.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	var retry workflow.Run
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/retry", "", 202, &retry)
	if retry.RetryOf == nil || *retry.RetryOf != id {
		t.Fatalf("retry: %+v", retry)
	}
	done := r.await(t, retry.ID, runstore.StatusCompleted)
	if string(done.Result) != `{"checked":true}` {
		t.Fatalf("retry output: %s", done.Result)
	}
	checked := 0
	for _, name := range r.children(t, r.list) {
		if name == "Checked" {
			checked++
		}
	}
	if checked != 1 {
		t.Fatalf("the retry repeated a request the failed run made: %d", checked)
	}
}

func TestReplacingRunsCancelTheActiveRun(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Watch", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "item.updated", collection_id: "`+r.list+`" }],
  concurrency: "replace",
  async run(ctx) {
    await ctx.sleep(60 * 60 * 1000);
  },
});
`)
	itemID := r.item(t, "Watched")
	rename := func(name string) {
		t.Helper()
		var item struct {
			Version int `json:"version"`
		}
		r.call(t, "GET", "/v1/resources/"+itemID, "", 200, &item)
		if _, err := r.s.Update(t.Context(), "alice", itemID, item.Version, dms.UpdateResource{Name: &name}); err != nil {
			t.Fatal(err)
		}
	}
	runsOf := func(n int) []workflow.Run {
		t.Helper()
		var page struct {
			Value []workflow.Run `json:"value"`
		}
		for deadline := time.Now().Add(20 * time.Second); len(page.Value) < n; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("runs: %+v", page.Value)
			}
			r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+w.ID, "", 200, &page)
		}
		return page.Value
	}
	rename("First")
	first := runsOf(1)[0].ID
	sleeping := r.await(t, first, runstore.StatusWaiting)
	if sleeping.WaitingUntil == nil || sleeping.WaitingUntil.Before(time.Now().Add(50*time.Minute)) {
		t.Fatalf("a sleeping run: %+v", sleeping)
	}
	rename("Second")
	runs := runsOf(2)
	second := runs[0].ID
	if second == first {
		second = runs[1].ID
	}
	r.await(t, first, runstore.StatusCancelled)
	r.await(t, second, runstore.StatusWaiting)

	// Retention deletes finished runs with their operations, and keeps
	// those still going.
	if err := r.runs.Collect(t.Context(), time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.call(t, "GET", "/v1/workflow-runs/"+first, "", 404, nil)
	r.await(t, second, runstore.StatusWaiting)
	if _, err := r.s.DAL.Operations().Find(t.Context(), first); err == nil {
		t.Fatal("retention kept the run's operation")
	}
	pending, err := r.s.DAL.Operations().List(t.Context(), operations.Filter{Kinds: []string{dms.OperationKindRun}, Statuses: []operations.Status{operations.StatusPending}})
	if err != nil || len(pending) != 1 || pending[0].ID != second {
		t.Fatalf("pending runs: %+v %v", pending, err)
	}
}
