package runner

import (
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func TestForEachRunsNestedLoopsInOrder(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Loops", `{
		"triggers": [{"type": "manual"}],
		"flow": {"start": "outer", "nodes": {
			"outer": {"activity": "forEach", "inputs": {"items": [1, 2], "as": "o"}, "next": {"body": "inner", "done": "none"}},
			"inner": {"activity": "forEach", "inputs": {"items": ["a", "b"], "as": "i"}, "next": {"body": "log"}},
			"log": {"activity": "set_variable", "inputs": {"name": "log", "value": "{var:log}{var:o}{var:i}{var:i_index};"}},
			"none": {"activity": "forEach", "inputs": {"items": [], "as": "x"}, "next": {"body": "never", "done": "last"}},
			"never": {"activity": "fail", "inputs": {"message": "an empty loop ran its body"}},
			"last": {"activity": "set_variable", "inputs": {"name": "log", "value": "{var:log}end"}}
		}}
	}`)
	if _, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, w.ID, 1)
	run, err := f.r.Run(ctx, "alice", runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "completed" {
		t.Fatalf("run %s: %s", run.Status, run.Error)
	}
	vars := run.Result.(map[string]any)["variables"].(map[string]any)
	if got, want := vars["log"], "1a0;1b1;2a0;2b1;end"; got != want {
		t.Fatalf("loops logged %q, want %q", got, want)
	}
}

func TestConcurrencySkipAndReplace(t *testing.T) {
	f := setup(t)
	def := func(policy string) string {
		return `{"triggers": [{"type": "item.updated", "collection_id": "$LIST"}], "concurrency": "` + policy + `",
			"flow": {"start": "wait", "nodes": {"wait": {"activity": "delay", "inputs": {"duration": "1s"}}}}}`
	}
	skip := f.workflow(t, "Skip", def("skip"))
	replace := f.workflow(t, "Replace", def("replace"))
	parallel := f.workflow(t, "Parallel", def("parallel"))
	item := f.item(t, "alice", "Contract", nil)
	for i, note := range []string{"first", "second"} {
		r := f.head(t, item.ID)
		if _, err := f.dms.Update(ctx, "alice", item.ID, r.Version, dms.UpdateResource{Values: &map[string]any{"note": note}}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Let the first run start before the second change.
			time.Sleep(300 * time.Millisecond)
		}
	}
	if got := f.runs(t, skip.ID, 1); got[0].Status != "completed" {
		t.Errorf("skip: %+v", got)
	}
	states := map[string]int{}
	for _, run := range f.runs(t, replace.ID, 2) {
		states[run.Status]++
	}
	if states["cancelled"] != 1 || states["completed"] != 1 {
		t.Errorf("replace: %v", states)
	}
	for _, run := range f.runs(t, parallel.ID, 2) {
		if run.Status != "completed" {
			t.Errorf("parallel: %+v", run)
		}
	}
}
