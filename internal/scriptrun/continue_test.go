package scriptrun

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/workflow"
)

// A run that pages through a list continues as new after each page: its
// journal holds one page at a time, it keeps its ID, and the run that
// called it receives what its last generation returned.
func TestRunsContinueAsNewPageByPage(t *testing.T) {
	r := run(t)
	for i := range 25 {
		r.item(t, fmt.Sprintf("Invoice %02d", i))
	}
	pager := r.workflow(t, "Count pages", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "call" }],
  async run(ctx) {
    const state = (ctx.state ?? { counted: 0 }) as { cursor?: string; counted: number };
    const page = await ctx.api.v1.resources.byId("`+r.list+`").children.get({ queryParameters: { top: 10, skiptoken: state.cursor } });
    const counted = state.counted + page!.value!.length;
    const next = /[?&]%24skiptoken=([^&]+)/.exec(page!.odataNextLink ?? "");
    if (next) ctx.continueAsNew({ cursor: decodeURIComponent(next[1]), counted });
    return { counted, generation: ctx.generation };
  },
});
`)
	caller := r.workflow(t, "Count", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    return await ctx.call("`+pager.Key+`");
  },
});
`)
	done := r.await(t, r.start(t, caller, nil), runstore.StatusCompleted)
	var result struct{ Counted, Generation int }
	if err := json.Unmarshal(done.Result, &result); err != nil || result.Counted != 25 || result.Generation != 2 {
		t.Fatalf("output: %s", done.Result)
	}
	var page struct {
		Value []workflow.Run `json:"value"`
	}
	r.call(t, "GET", "/v1/workspaces/"+r.ws+"/workflow-runs?workflow_id="+pager.ID, "", 200, &page)
	if len(page.Value) != 1 {
		t.Fatalf("%d runs of the pager", len(page.Value))
	}
	var pages workflow.Run
	r.call(t, "GET", "/v1/workflow-runs/"+page.Value[0].ID, "", 200, &pages)
	continued := 0
	for _, entry := range pages.Logs {
		if strings.HasPrefix(entry.Message, "Continued as new") {
			continued++
		}
	}
	if pages.Generation != 2 || len(pages.Steps) != 1 || continued != 2 || pages.State == nil {
		t.Fatalf("pager run: generation %d, %d steps, %d continuations, state %v", pages.Generation, len(pages.Steps), continued, pages.State)
	}
}

// A retry of a run that failed in a later generation goes on from that
// generation, with its state.
func TestRetriesGoOnFromTheFailedGeneration(t *testing.T) {
	r := run(t)
	w := r.workflow(t, "Flaky loop", `import { workflow } from "@papergo/workflow";

export default workflow({
  triggers: [{ type: "manual" }],
  async run(ctx) {
    const { n } = (ctx.state ?? { n: 0 }) as { n: number };
    if (n === 2 && !ctx.run.id.startsWith("retry:")) throw new Error("generation 2 failed");
    if (n < 3) ctx.continueAsNew({ n: n + 1 });
    return { n, generation: ctx.generation, retried: ctx.run.id.startsWith("retry:") };
  },
});
`)
	id := r.start(t, w, nil)
	failed := r.await(t, id, runstore.StatusFailed)
	if failed.Generation != 2 || !strings.Contains(failed.Error, "generation 2 failed") {
		t.Fatalf("failed run: %+v", failed)
	}
	var retry workflow.Run
	r.call(t, "POST", "/v1/workflow-runs/"+id+"/retry", "", 202, &retry)
	done := r.await(t, retry.ID, runstore.StatusCompleted)
	var result struct {
		N, Generation int
		Retried       bool
	}
	if err := json.Unmarshal(done.Result, &result); err != nil || result.N != 3 || result.Generation != 3 || !result.Retried {
		t.Fatalf("retry output: %s", done.Result)
	}
}
