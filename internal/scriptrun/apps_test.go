package scriptrun

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// Another app starts a manual workflow as a member who does not manage the
// workspace: a retry with its idempotency key returns the same run, and it
// reads the run's status and result, but not its steps or log.
func TestAppsStartManualRunsAndReadTheirResults(t *testing.T) {
	r := run(t)
	if err := r.s.SetWorkspaceMember(t.Context(), "alice", r.ws, dms.WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	itemID := r.item(t, "Contract 7")
	w := r.workflow(t, "Summarize", `import { workflow } from "@papergo/workflow";

export default workflow<{ style: string }>({
  triggers: [{ type: "manual", collection_id: "`+r.list+`" }],
  input_schema: { type: "object", properties: { style: { type: "string", default: "short" } } },
  async run(ctx, input) {
    const item = await ctx.api.v1.resources.byId(ctx.itemId!).get();
    ctx.log("summarizing");
    return { summary: input.style + ": " + item!.name };
  },
});
`)
	ctx, err := dms.WithIdempotencyKey(t.Context(), "order-42")
	if err != nil {
		t.Fatal(err)
	}
	start := workflow.Start{ItemIDs: []string{itemID}, Inputs: map[string]any{"style": "long"}}
	first, err := r.wf.StartRuns(ctx, "bob", w.ID, start)
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	again, err := r.wf.StartRuns(ctx, "bob", w.ID, start)
	if err != nil || len(again) != 1 || again[0] != first[0] {
		t.Fatalf("a retry started %v, want %v: %v", again, first, err)
	}
	if _, err := r.wf.StartRuns(ctx, "bob", w.ID, workflow.Start{ItemIDs: []string{itemID}}); !errors.Is(err, dms.ErrIdempotencyConflict) {
		t.Fatalf("the key with another start: %v", err)
	}
	if queued, err := r.wf.Run(t.Context(), "bob", first[0]); err != nil || queued.ID != first[0] {
		t.Fatalf("the starter's queued run: %+v %v", queued, err)
	}
	r.await(t, first[0], runstore.StatusCompleted)
	seen, err := r.wf.Run(t.Context(), "bob", first[0])
	if err != nil || seen.Status != runstore.StatusCompleted || summary(t, seen.Result) != "long: Contract 7" || seen.Inputs["style"] != "long" {
		t.Fatalf("the starter's run: %+v %v", seen, err)
	}
	if len(seen.Steps) != 0 || len(seen.Logs) != 0 {
		t.Fatalf("the starter sees the run's steps or log: %+v", seen)
	}
	if managed, err := r.wf.Run(t.Context(), "alice", first[0]); err != nil || len(managed.Steps) == 0 || len(managed.Logs) == 0 {
		t.Fatalf("the manager's run: %+v %v", managed, err)
	}
	if _, err := r.wf.Run(t.Context(), "carol", first[0]); err == nil {
		t.Fatal("someone else read the run")
	}

	// Over HTTP: start with an idempotency key, then wait for the result.
	var started struct {
		RunIDs []string `json:"run_ids"`
	}
	req := httptest.NewRequest("POST", "/v1/workflows/"+w.ID+"/runs", strings.NewReader(`{"item_ids":["`+itemID+`"]}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "http-1")
	rec := httptest.NewRecorder()
	r.handler.ServeHTTP(rec, req)
	if rec.Code != 202 || json.Unmarshal(rec.Body.Bytes(), &started) != nil || len(started.RunIDs) != 1 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var done workflow.Run
	r.call(t, "GET", "/v1/workflow-runs/"+started.RunIDs[0]+"?wait=20", "", 200, &done)
	if done.Status != runstore.StatusCompleted || summary(t, done.Result) != "short: Contract 7" {
		t.Fatalf("waited run: %+v", done)
	}
	r.call(t, "GET", "/v1/workflow-runs/"+started.RunIDs[0]+"?wait=60", "", 422, nil)
}

func summary(t *testing.T, result json.RawMessage) string {
	t.Helper()
	var out struct{ Summary string }
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	return out.Summary
}
