package scriptrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dms"
	"papergo/internal/documentfiles"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
)

// files mounts the document files module with an OCR engine.
func files(s *dms.Service, store storage.Store) httpapi.Module {
	m := &documentfiles.Maker{Storage: store, OCR: receiptText{}, MaxFileBytes: 32 << 20}
	if err := m.Register(s); err != nil {
		panic(err)
	}
	return m.Module()
}

// reviewSource optimizes the run's photo, stages the copy and waits for a
// review of it, after leaving a follow-up task it does not wait for.
const reviewSource = `import { plain, workflow } from "@papergo/workflow";
export default workflow({
  triggers: [{ type: "manual", collection_id: "%s" }],
  async run(ctx) {
    const optimized = plain((await ctx.wait((await ctx.api.v1.items.byId(ctx.itemId!).optimizeImage.post({}))!)).result) as { artifact_id?: string };
    if (!optimized.artifact_id) throw new Error("nothing made");
    const staged = (await ctx.api.v1.artifacts.byId(optimized.artifact_id).stage.post())!;
    const tasks = ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks;
    await tasks.post({ itemId: ctx.itemId, title: "Follow up" });
    const review = await tasks.post({ kind: "approval", itemId: ctx.itemId, title: "Review",
      review: { type: "file.compare", stagedFileId: staged.id } });
    await ctx.wait(review!.operationId!);
  },
});
`

// photo uploads a large photo to library and returns its item.
func (r *runtime) photo(t *testing.T, library, name string) string {
	t.Helper()
	o, err := r.blobs.Put(t.Context(), bytes.NewReader(receiptPhoto(t, 1200, 800)), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	item, _, err := r.s.PutFile(t.Context(), "alice", library, []string{name}, dms.PutFile{Blob: dms.BlobInput{ObjectKey: o.Key, Filename: name, ContentType: "image/png", Size: o.Size, SHA256: o.SHA256}})
	if err != nil {
		t.Fatal(err)
	}
	return item.ID
}

// openTasks counts the open tasks f selects.
func (r *runtime) openTasks(t *testing.T, f taskstore.Filter) int {
	t.Helper()
	f.Status = taskstore.StatusOpen
	rows, err := r.s.DAL.Tasks().List(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// awaitTasks waits until f selects n open tasks.
func (r *runtime) awaitTasks(t *testing.T, f taskstore.Filter, n int) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); r.openTasks(t, f) != n; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d open tasks, want %d", r.openTasks(t, f), n)
		}
	}
}

// pendingStaged counts the pending staged files of an item.
func (r *runtime) pendingStaged(t *testing.T, item string) int {
	t.Helper()
	return daltest.Count(t, r.s.DAL, "staged_files", daltest.Row{"item_id": item, "state": string(blobs.StagedPending)})
}

// Deleting an item cancels the runs on it, the tasks about it and the
// files staged to replace its file.
func TestDeletingAnItemCancelsItsWorkflowWork(t *testing.T) {
	r := run(t, files)
	library, err := r.s.Create(t.Context(), "alice", r.ws, dms.CreateResource{Kind: "library", Name: "Photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w := r.workflow(t, "Review photos", fmt.Sprintf(reviewSource, library.ID))
	item := r.photo(t, library.ID, "photo.png")
	id := r.start(t, w, nil, item)
	r.awaitTasks(t, taskstore.Filter{ItemID: item}, 2)
	r.call(t, "POST", "/v1/workspaces/"+r.ws+"/tasks", `{"item_id":"`+item+`","title":"Check by hand"}`, 201, nil)
	if n := r.openTasks(t, taskstore.Filter{ItemID: item}); n != 3 || r.pendingStaged(t, item) != 1 {
		t.Fatalf("before the delete: %d open tasks, %d staged files", n, r.pendingStaged(t, item))
	}

	var current struct{ Version int }
	r.call(t, "GET", "/v1/resources/"+item, "", 200, &current)
	if err = r.s.Delete(t.Context(), "alice", item, current.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	r.await(t, id, runstore.StatusCancelled)
	if n := r.openTasks(t, taskstore.Filter{ItemID: item}); n != 0 {
		t.Fatalf("%d tasks about the deleted item are open", n)
	}
	if n := r.pendingStaged(t, item); n != 0 {
		t.Fatalf("%d staged files of the deleted item are pending", n)
	}
}

// Deleting a workflow cancels its active runs, its open tasks and their
// staged files, including tasks of runs that already ended.
func TestDeletingAWorkflowCancelsItsWork(t *testing.T) {
	r := run(t, files)
	library, err := r.s.Create(t.Context(), "alice", r.ws, dms.CreateResource{Kind: "library", Name: "Photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	w := r.workflow(t, "Review photos", fmt.Sprintf(reviewSource, library.ID))
	item := r.photo(t, library.ID, "photo.png")
	id := r.start(t, w, nil, item)
	r.awaitTasks(t, taskstore.Filter{WorkflowID: w.ID}, 2)
	r.await(t, id, runstore.StatusWaiting)
	current, err := r.wf.Get(t.Context(), "alice", w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.wf.Delete(t.Context(), "alice", w.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	r.await(t, id, runstore.StatusCancelled)
	if n := r.openTasks(t, taskstore.Filter{WorkflowID: w.ID}); n != 0 {
		t.Fatalf("%d tasks of the deleted workflow are open", n)
	}
	if n := r.pendingStaged(t, item); n != 0 {
		t.Fatalf("%d staged files of the deleted workflow are pending", n)
	}
}

// Deleting a workspace cancels the runs of its workflows that have no item.
func TestDeletingAWorkspaceCancelsItsRuns(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Wait", `import { workflow } from "@papergo/workflow";
export default workflow({ triggers: [{ type: "manual" }], async run(ctx) { await ctx.sleep(3600 * 1000); } });
`)
	id := r.start(t, w, nil)
	r.await(t, id, runstore.StatusWaiting)
	var ws struct{ Version int }
	r.call(t, "GET", "/v1/resources/"+r.ws, "", 200, &ws)
	if err := r.s.Delete(t.Context(), "alice", r.ws, ws.Version, dms.DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	for deadline := 0; ; deadline++ {
		run, err := r.s.DAL.Runs().Find(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == runstore.StatusCancelled {
			return
		}
		if deadline > 1000 {
			t.Fatalf("the run of a deleted workspace is %s", run.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ownershipSource leaves a task it owns and waits for an operation it was
// given, which it only observes; without one, it waits for its own task.
const ownershipSource = `import { workflow } from "@papergo/workflow";
export default workflow<{ operation?: string }>({
  triggers: [{ type: "manual" }],
  input_schema: { type: "object", properties: { operation: { type: "string" } } },
  async run(ctx, input) {
    const task = await ctx.api.v1.workspaces.byId(ctx.run.workspace_id).tasks.post({ title: "Owned" });
    await ctx.wait(input?.operation ?? task!.operationId!);
  },
});
`

// Cancelling a run cancels the work it started, which it owns, and leaves
// the operations it only waits for, such as another run's task.
func TestCancellingARunCancelsWhatItOwns(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Owner", ownershipSource)
	other := r.start(t, w, nil)
	r.awaitTasks(t, taskstore.Filter{WorkspaceID: r.ws}, 1)
	shared, err := r.s.OwnedOperations(t.Context(), other)
	if err != nil || len(shared) != 1 {
		t.Fatalf("owned by the other run %v: %v", shared, err)
	}
	id := r.start(t, w, map[string]any{"operation": shared[0]})
	r.awaitTasks(t, taskstore.Filter{WorkspaceID: r.ws}, 2)
	r.await(t, id, runstore.StatusWaiting)
	owned, err := r.s.OwnedOperations(t.Context(), id)
	if err != nil || len(owned) != 1 || owned[0] == shared[0] {
		t.Fatalf("owned %v: %v", owned, err)
	}
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/cancel", "", 200, nil)
	r.await(t, id, runstore.StatusCancelled)
	for op, want := range map[string]operations.Status{owned[0]: operations.StatusCancelled, shared[0]: operations.StatusPending} {
		got, err := r.s.DAL.Operations().Find(t.Context(), op)
		if err != nil || got.Status != want {
			t.Fatalf("%s is %s, want %s: %v", op, got.Status, want, err)
		}
	}
	r.await(t, other, runstore.StatusWaiting)
	if n := r.openTasks(t, taskstore.Filter{WorkspaceID: r.ws}); n != 1 {
		t.Fatalf("%d open tasks, want the other run's", n)
	}
}

// An optimization belongs to no workflow: a client without a run gets an
// artifact, an identical request reuses it, and the client stages and
// promotes it itself. A workflow run cannot promote the client's file.
func TestArtifactsWithoutRuns(t *testing.T) {
	r := run(t, files)
	library, err := r.s.Create(t.Context(), "alice", r.ws, dms.CreateResource{Kind: "library", Name: "Photos", WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := r.photo(t, library.ID, "photo.png")
	optimize := func() documentfiles.OptimizeResult {
		t.Helper()
		var op dms.OperationView
		r.call(t, "POST", "/v1/items/"+item+"/optimize-image", "", 202, &op)
		for deadline := time.Now().Add(20 * time.Second); op.Status == operations.StatusPending; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the optimization did not finish")
			}
			r.call(t, "GET", "/v1/operations/"+op.ID, "", 200, &op)
		}
		var res documentfiles.OptimizeResult
		if op.Status != operations.StatusSucceeded || json.Unmarshal(op.Result, &res) != nil || res.ArtifactID == "" {
			t.Fatalf("%+v", op)
		}
		return res
	}
	first := optimize()
	again := optimize()
	if first.Reused || !again.Reused || again.ArtifactID != first.ArtifactID || again.Metrics.Bytes != first.Metrics.Bytes {
		t.Fatalf("not reused: %+v %+v", first, again)
	}
	var artifact documentfiles.ArtifactView
	r.call(t, "GET", "/v1/artifacts/"+first.ArtifactID, "", 200, &artifact)
	if artifact.ItemID != item || artifact.ContentType != "image/webp" || len(artifact.Sources) != 1 || !artifact.ExpiresAt.After(time.Now()) {
		t.Fatalf("%+v", artifact)
	}
	var staged documentfiles.StagedResult
	r.call(t, "POST", "/v1/artifacts/"+first.ArtifactID+"/stage", "", 201, &staged)
	if staged.RunID != "" || staged.ArtifactID != first.ArtifactID {
		t.Fatalf("%+v", staged)
	}
	if r.pendingStaged(t, item) != 1 {
		t.Fatal("the staged file is not pending")
	}
	var promoted documentfiles.PromoteResult
	r.call(t, "POST", "/v1/staged-files/"+staged.ID+"/promote", "{}", 200, &promoted)
	if promoted.FileVersion != 2 {
		t.Fatalf("%+v", promoted)
	}
}
