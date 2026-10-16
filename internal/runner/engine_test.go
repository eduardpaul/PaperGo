package runner

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
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

// Loop passes have their own node budget: a loop over hundreds of items
// with a body of several nodes runs to the end.
func TestForEachPassesHaveTheirOwnNodeBudget(t *testing.T) {
	f := setup(t)
	items := make([]string, 600)
	for i := range items {
		items[i] = strconv.Itoa(i)
	}
	w := f.workflow(t, "Loop", `{
		"triggers": [{"type": "manual"}],
		"flow": {"start": "loop", "nodes": {
			"loop": {"activity": "forEach", "inputs": {"items": [`+strings.Join(items, ",")+`], "as": "n"}, "next": {"body": "a"}},
			"a": {"activity": "set_variable", "inputs": {"name": "last", "value": "{var:n}"}, "next": {"done": "b"}},
			"b": {"activity": "set_variable", "inputs": {"name": "passes", "value": "{var:n_index}"}}
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
	if got := run.Result.(map[string]any)["variables"].(map[string]any)["last"]; got != "599" && got != json.Number("599") {
		t.Fatalf("last %v", got)
	}
}

// A cycle without a loop still stops.
func TestACycleStopsAtTheNodeLimit(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Cycle", `{
		"triggers": [{"type": "manual"}],
		"flow": {"start": "a", "nodes": {
			"a": {"activity": "set_variable", "inputs": {"name": "x", "value": "1"}, "next": {"done": "b"}},
			"b": {"activity": "set_variable", "inputs": {"name": "x", "value": "2"}, "next": {"done": "a"}}
		}}
	}`)
	if _, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, w.ID, 1)
	if runs[0].Status != "failed" || !strings.Contains(runs[0].Error, "nodes") {
		t.Fatalf("run %s: %s", runs[0].Status, runs[0].Error)
	}
}

func TestConcurrencySkipAndReplace(t *testing.T) {
	f := setup(t)
	def := func(policy string) string {
		return `{"triggers": [{"type": "item.updated", "collection_id": "$LIST"}], "concurrency": "` + policy + `",
			"flow": {"start": "wait", "nodes": {"wait": {"activity": "delay", "inputs": {"duration": "5s"}}}}}`
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
			// Wait until each first run is inside its delay before triggering again.
			for _, w := range []workflow.Workflow{skip, replace, parallel} {
				deadline := time.Now().Add(20 * time.Second)
				started := false
				for !started {
					page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: w.ID, Limit: 1})
					if err != nil {
						t.Fatal(err)
					}
					if len(page.Data) == 1 {
						run, err := f.r.Run(ctx, "alice", page.Data[0].ID)
						if err != nil {
							t.Fatal(err)
						}
						for _, step := range run.Steps {
							if step.Name == "DBOS.sleep" && step.CompletedAt != nil && step.CompletedAt.After(time.Now()) {
								started = true
								break
							}
						}
					}
					if started {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("workflow %s did not start its first run: %+v", w.ID, page.Data)
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
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

// Coalesce starts nothing while a run on the item is queued, and otherwise
// queues one run that starts when the active one ends: a burst of changes
// costs at most two runs, one after the other.
func TestConcurrencyCoalesce(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	model := &runnerDecisionModel{probability: .9, during: func() { enteredOnce.Do(func() { close(entered) }); <-release }}
	f := setup(t, func(tools *workflow.Tools) { tools.Decisions = model })
	w := f.workflow(t, "Coalesce", `{"triggers":[{"type":"item.updated","collection_id":"$LIST"}],"concurrency":"coalesce",
      "flow":{"start":"decide","nodes":{"decide":{"activity":"ai.decide","inputs":{"input":"{item:values.note}","questions":[{"key":"route","instructions":"Which route?","options":[{"key":"a","text":"A"},{"key":"b","text":"B"}]}]}}}}}`)
	item := f.item(t, "alice", "Contract", nil)
	for i, note := range []string{"first", "second", "third"} {
		r := f.head(t, item.ID)
		if _, err := f.dms.Update(ctx, "alice", item.ID, r.Version, dms.UpdateResource{Values: &map[string]any{"note": note}}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			select {
			case <-entered:
			case <-time.After(20 * time.Second):
				t.Fatal("first run did not enter heavy work")
			}
		}
	}
	// Drain the whole burst while inference is explicitly held. The first run
	// cannot finish between metadata writes, even with SQLite race instrumentation.
	if _, err := f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: w.ID, Limit: 100})
	if err != nil || len(page.Data) != 2 {
		t.Fatal("coalesced burst", page.Data, err)
	}
	unblock()
	runs := f.runs(t, w.ID, 2)
	details := make([]Run, len(runs))
	for i, run := range runs {
		if run.Status != "completed" {
			t.Fatalf("coalesce run: %+v", run)
		}
		var err error
		if details[i], err = f.r.Run(ctx, "alice", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, second := details[0], details[1]
	if first.CreatedAt.After(second.CreatedAt) {
		first, second = second, first
	}
	ended := first.Steps[len(first.Steps)-1].CompletedAt
	started := second.Steps[0].StartedAt
	if ended == nil || started == nil || started.Before(*ended) {
		t.Fatalf("the follow-up started before the first run ended: %v %v", ended, started)
	}
}

func TestFolderEvents(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Folders", `{"triggers": [
		{"type": "folder.created", "collection_id": "$LIST"}, {"type": "folder.updated", "collection_id": "$LIST"},
		{"type": "folder.deleted", "collection_id": "$LIST"}, {"type": "folder.restored", "collection_id": "$LIST"},
		{"type": "folder.purged", "collection_id": "$LIST"}],
		"flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	items := f.workflow(t, "Items", `{"triggers": [{"type": "item.created", "collection_id": "$LIST"}, {"type": "item.deleted", "collection_id": "$LIST"}],
		"flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	a, err := f.dms.Create(ctx, "alice", f.list.ID, dms.CreateResource{Kind: "folder", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.dms.Create(ctx, "alice", f.list.ID, dms.CreateResource{Kind: "folder", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	name := "A2"
	if a, err = f.dms.Update(ctx, "alice", a.ID, a.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if a, err = f.dms.MoveItem(ctx, "alice", a.ID, a.Version, dms.MoveItem{CollectionID: f.list.ID, ParentID: b.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.Create(ctx, "alice", a.ID, dms.CreateResource{Kind: "item", Name: "Inside"}); err != nil {
		t.Fatal(err)
	}
	b = f.head(t, b.ID)
	if err = f.dms.Delete(ctx, "alice", b.ID, b.Version, dms.DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.RestoreDeleted(ctx, "alice", f.list.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	b = f.head(t, b.ID)
	if err = f.dms.Delete(ctx, "alice", b.ID, b.Version, dms.DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if err = f.dms.PurgeDeleted(ctx, "alice", f.list.ID, b.ID, true); err != nil {
		t.Fatal(err)
	}
	// Created A and B, renamed and moved A; deleted, restored, deleted and
	// purged both.
	counts := map[string]int{}
	for _, run := range f.runs(t, w.ID, 12) {
		if run.Status != "completed" {
			t.Fatalf("run: %+v", run)
		}
		counts[run.EventType]++
		hasItem := run.ItemID != nil
		if wantItem := run.EventType == "folder.created" || run.EventType == "folder.updated"; hasItem != wantItem && run.EventType != "folder.restored" {
			t.Fatalf("%s run item: %v", run.EventType, run.ItemID)
		}
	}
	want := map[string]int{"folder.created": 2, "folder.updated": 2, "folder.deleted": 4, "folder.restored": 2, "folder.purged": 2}
	for typ, n := range want {
		if counts[typ] != n {
			t.Fatalf("folder events %v, want %v", counts, want)
		}
	}
	// Folder writes start no item workflows; the item inside still does.
	f.runs(t, items.ID, 3)
}

func TestManualCoalescingPreservesEachLaunch(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	item := f.item(t, "alice", "Manual item", nil)
	w := f.workflow(t, "Manual coalesce", `{"triggers":[{"type":"manual","collection_id":"$LIST"}],"concurrency":"coalesce","input_schema":{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}},"flow":{"start":"save","nodes":{"save":{"activity":"set_variable","inputs":{"name":"value","value":"{input:value}"}}}}}`)
	var runs []string
	for _, value := range []string{"first", "second"} {
		ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}, Inputs: map[string]any{"value": value}})
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, ids[0])
	}
	for _, id := range runs {
		if run := f.wait(t, id, finished); run.Status != "completed" {
			t.Fatal("manual run was dropped", run)
		}
	}
	f.runs(t, w.ID, 2)
}

func TestManualSkipKeepsAnInspectableRejection(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	item := f.item(t, "alice", "Manual item", nil)
	w := f.workflow(t, "Manual skip", `{"triggers":[{"type":"manual","collection_id":"$LIST"}],"concurrency":"skip","flow":{"start":"hold","nodes":{"hold":{"activity":"delay","inputs":{"duration":"1h"}}}}}`)
	first, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.wait(t, second[0], finished); run.Status != "failed" || !strings.Contains(run.Error, "another run is active") {
		t.Fatal("manual skip lost its rejection", run)
	}
	if _, err = f.r.Cancel(ctx, "alice", first[0]); err != nil {
		t.Fatal(err)
	}
}
