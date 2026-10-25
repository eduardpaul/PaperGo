package scriptrun

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// bobWorkflow creates a published workflow that bob, a workspace owner,
// wrote.
func (r *runtime) bobWorkflow(t *testing.T, name, source string) workflow.Workflow {
	t.Helper()
	if err := r.s.SetWorkspaceMember(t.Context(), "alice", r.ws, dms.WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	w, err := r.wf.Create(t.Context(), "bob", r.ws, workflow.Save{Name: name, Source: source, AutomaticEnabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func (r *runtime) retry(t *testing.T, id string) string {
	t.Helper()
	var retried workflow.Run
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/retry", "", 202, &retried)
	return retried.ID
}

// A retry of a run that called another author's workflow still receives
// what the called run returned.
func TestRetriesKeepTheRunsTheirRunCalled(t *testing.T) {
	r := run(t)
	called := r.bobWorkflow(t, "Answer", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run() { return { answer: 42 }; },
});
`)
	caller := r.workflow(t, "Ask", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    const out = await ctx.call<{ answer: number }>("`+called.Key+`");
    if (!ctx.run.id.startsWith("retry:")) throw new Error("the first attempt fails after its call");
    return out;
  },
});
`)
	id := r.start(t, caller, nil)
	r.await(t, id, runstore.StatusFailed)
	done := r.await(t, r.retry(t, id), runstore.StatusCompleted)
	if !strings.Contains(string(done.Result), "42") {
		t.Fatalf("retry output: %s", done.Result)
	}
}

// Retrying a run retries the runs it called that failed, in their place,
// and replays those that succeeded.
func TestRetriesRetryTheCallsThatFailed(t *testing.T) {
	r := run(t)
	good, bad := r.item(t, "Good"), r.item(t, "Bad")
	called := r.bobWorkflow(t, "Check", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call", collection_id: "`+r.list+`" }],
  async run(ctx) {
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    if (item!.name === "Bad") throw new Error("bad item");
    return { name: item!.name };
  },
});
`)
	caller := r.workflow(t, "Check all", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    const items = ["`+good+`", "`+bad+`"];
    const out = await Promise.all(items.map((id) => ctx.call<{ name: string }>("`+called.Key+`", {}, { itemId: id })));
    return { names: out.map((o) => o.name) };
  },
});
`)
	id := r.start(t, caller, nil)
	r.await(t, id, runstore.StatusFailed)
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+called.ID, "", 200, &page)
	if len(page.Value) != 2 {
		t.Fatalf("%d called runs", len(page.Value))
	}
	// Fix the item, then retry the caller.
	item, err := r.s.Get(t.Context(), "alice", bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Update(t.Context(), "alice", bad, item.Version, dms.UpdateResource{Name: new("Fixed")}); err != nil {
		t.Fatal(err)
	}
	done := r.await(t, r.retry(t, id), runstore.StatusCompleted)
	var result struct{ Names []string }
	if err := json.Unmarshal(done.Result, &result); err != nil || strings.Join(result.Names, ",") != "Good,Fixed" {
		t.Fatalf("retry output: %s", done.Result)
	}
	// The call that succeeded replayed; the one that failed ran again.
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+called.ID, "", 200, &page)
	if len(page.Value) != 3 {
		t.Fatalf("%d called runs after the retry, want 3", len(page.Value))
	}
}

// A call that would wait for one of its callers, as a coalescing run on the
// item its caller works on does, is refused instead of waiting forever.
func TestCallsThatWouldWaitForTheirCallerAreRefused(t *testing.T) {
	r := run(t)
	itemID := r.item(t, "Contract")
	w := r.workflow(t, "Index", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual", collection_id: "`+r.list+`" }, { type: "call", collection_id: "`+r.list+`" }],
  concurrency: "coalesce",
  async run(ctx) {
    if (ctx.trigger?.kind === "call") return "called";
    return await ctx.call(ctx.run.workflow.key, {}, { itemId: ctx.itemId });
  },
});
`)
	id := r.start(t, w, nil, itemID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		var run workflow.Run
		r.call(t, "GET", "/v1/workflow-runs/"+id, "", 200, &run)
		if run.Status == runstore.StatusFailed {
			if !strings.Contains(run.Error, "its caller") {
				t.Fatalf("the call failed with %q", run.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the run is %s: the call waits for its caller", run.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A test run's calls are test runs: they raise no workflow events.
func TestTestRunsCallTestRuns(t *testing.T) {
	r := run(t)
	called := r.workflow(t, "Called", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run() { return { ok: true }; },
});
`)
	follower := r.workflow(t, "Follow", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "wf.`+called.Key+`.completed" }],
  async run() { return {}; },
});
`)
	caller := r.workflow(t, "Caller", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) { return await ctx.call("`+called.Key+`"); },
});
`)
	id, err := r.wf.StartTest(t.Context(), "alice", caller.ID, workflow.TestRun{})
	if err != nil {
		t.Fatal(err)
	}
	r.await(t, id, runstore.StatusCompleted)
	time.Sleep(500 * time.Millisecond)
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+follower.ID, "", 200, &page)
	if len(page.Value) != 0 {
		t.Fatalf("a test run's call started %d followers", len(page.Value))
	}
}

// A retry goes down the calls: the failed run its failed call called is
// retried too.
func TestRetriesRetryFailedCallsAllTheWayDown(t *testing.T) {
	r := run(t)
	leaf := r.workflow(t, "Leaf", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run(ctx) {
    if (!ctx.run.id.startsWith("retry:")) throw new Error("the first leaf fails");
    return { leaf: true };
  },
});
`)
	middle := r.workflow(t, "Middle", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run(ctx) { return await ctx.call("`+leaf.Key+`"); },
});
`)
	top := r.workflow(t, "Top", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) { return await ctx.call("`+middle.Key+`"); },
});
`)
	id := r.start(t, top, nil)
	r.await(t, id, runstore.StatusFailed)
	done := r.await(t, r.retry(t, id), runstore.StatusCompleted)
	if !strings.Contains(string(done.Result), "leaf") {
		t.Fatalf("retry output: %s", done.Result)
	}
}
