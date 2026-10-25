package scriptrun

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// A run fans out to runs of a workflow another author wrote: the calls
// start together, the caller waits for them in one step and receives what
// they returned, and the called runs belong to it.
func TestRunsCallWorkflowsOnManyItems(t *testing.T) {
	r := run(t)
	if err := r.s.SetWorkspaceMember(t.Context(), "alice", r.ws, dms.WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	const items = 30
	for i := range items {
		r.item(t, fmt.Sprintf("Invoice %02d", i))
	}
	called, err := r.wf.Create(t.Context(), "bob", r.ws, workflow.Save{Name: "Measure", Source: `import { workflow } from "@papergo/workflow";

export default workflow<{ factor: number }>({
  triggers: [{ type: "call", collection_id: "` + r.list + `" }],
  input_schema: { type: "object", required: ["factor"], properties: { factor: { type: "integer" } } },
  async run(ctx, input) {
    if (ctx.trigger?.kind !== "call") throw new Error("not called");
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    return { length: item!.name!.length * input.factor, parent: ctx.trigger.parent_run_id };
  },
});
`, AutomaticEnabled: new(true)})
	if err != nil {
		t.Fatal(err)
	}
	caller := r.workflow(t, "Fan out", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    const items = await ctx.api.v1.resources.byId("`+r.list+`").children.all({ queryParameters: { top: 500 } });
    const measured = await Promise.all(items.map((item) => ctx.call<{ length: number; parent: string }>("`+called.Key+`", { factor: 2 }, { itemId: item.id! })));
    return { runs: measured.length, total: measured.reduce((sum, m) => sum + m.length, 0), parents: new Set(measured.map((m) => m.parent)).size };
  },
});
`)
	id := r.start(t, caller, nil)
	done := r.await(t, id, runstore.StatusCompleted)
	var result struct{ Runs, Total, Parents int }
	if err := json.Unmarshal(done.Result, &result); err != nil || result.Runs != items || result.Total != items*len("Invoice 00")*2 || result.Parents != 1 {
		t.Fatalf("output: %s", done.Result)
	}
	var kinds []string
	for _, step := range done.Steps {
		kinds = append(kinds, step.Kind+" "+strings.Fields(step.Name)[0])
	}
	if strings.Join(kinds, ", ") != "request GET, request POST, wait operations" {
		t.Fatalf("the fan-out took more than a request and a wait: %v", kinds)
	}
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?origin=call&workflow_id="+called.ID+"&$top=100", "", 200, &page)
	if len(page.Value) != items {
		t.Fatalf("%d called runs", len(page.Value))
	}
	child := page.Value[0]
	var detail workflow.Run
	r.call(t, "GET", "/v1/workflow-runs/"+child.ID, "", 200, &detail)
	if detail.ParentRunID == nil || *detail.ParentRunID != id || detail.Depth != 1 || detail.EventType != workflow.TriggerCall || detail.Status != runstore.StatusCompleted {
		t.Fatalf("called run: %+v", detail)
	}
}

// Calls are refused for workflows without a call trigger, items outside the
// trigger's collection and input its form rejects, and cancelling a caller
// cancels the runs it called.
func TestCallsAreCheckedAndBelongToTheirCaller(t *testing.T) {
	r := run(t)
	itemID := r.item(t, "Contract")
	waits := r.workflow(t, "Review", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run(ctx) {
    return await ctx.ask({ title: "Check it", itemId: ctx.itemId });
  },
});
`)
	plain := r.workflow(t, "Plain", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run() { return {}; },
});
`)
	fails := func(name, call, want string) {
		t.Helper()
		w := r.workflow(t, name, `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) { return await `+call+`; },
});
`)
		failed := r.await(t, r.start(t, w, nil), runstore.StatusFailed)
		if !strings.Contains(failed.Error, want) {
			t.Fatalf("%s failed with %q, want %q", name, failed.Error, want)
		}
	}
	fails("No trigger", `ctx.call("`+plain.Key+`")`, "has no call trigger")
	fails("Unknown", `ctx.call("missing")`, "no published workflow missing")
	fails("Bad input", `ctx.call("`+waits.Key+`", { extra: 1 }, { itemId: "`+itemID+`" })`, "calls[0].input takes no values")

	caller := r.workflow(t, "Delegate", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) { return await ctx.call("`+waits.Key+`", {}, { itemId: "`+itemID+`" }); },
});
`)
	id := r.start(t, caller, nil)
	r.await(t, id, runstore.StatusWaiting)
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	for len(page.Value) == 0 || page.Value[0].Status != runstore.StatusWaiting {
		r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?origin=call&workflow_id="+waits.ID, "", 200, &page)
	}
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/cancel", "", 200, nil)
	r.await(t, id, runstore.StatusCancelled)
	r.await(t, page.Value[0].ID, runstore.StatusCancelled)
}

// A workflow that keeps calling itself stops at the depth limit, and the
// refusal reaches the first run as its called run's failure.
func TestCallsStopAtTheDepthLimit(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Recurse", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }, { type: "call" }],
  async run(ctx) {
    await ctx.call(ctx.run.workflow.key);
  },
});
`)
	failed := r.await(t, r.start(t, w, nil), runstore.StatusFailed)
	if !strings.Contains(failed.Error, "failed") {
		t.Fatalf("first run: %s", failed.Error)
	}
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?origin=call&workflow_id="+w.ID, "", 200, &page)
	deepest := 0
	for _, run := range page.Value {
		deepest = max(deepest, run.Depth)
		if run.Depth == workflow.MaxDepth-1 && !strings.Contains(run.Error, "calls nest at most 4 deep") {
			t.Fatalf("deepest run: %+v", run)
		}
	}
	if len(page.Value) != workflow.MaxDepth-1 || deepest != workflow.MaxDepth-1 {
		t.Fatalf("%d called runs, deepest %d", len(page.Value), deepest)
	}
}
