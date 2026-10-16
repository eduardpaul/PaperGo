package runner

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"papergo/internal/workflow"
)

func init() {
	workflow.RegisterActivity(workflow.ActivitySpec{Key: "test.cyclewait", Kind: "action", Prepare: func(s workflow.Step, _ map[string]any) (workflow.Prepared, error) {
		return workflow.Prepared{Park: &workflow.Park{Kind: "test.cyclewait", Key: s.ExecutionID()}}, nil
	}})
	workflow.RegisterActivity(workflow.ActivitySpec{
		Key: "test.resumeWork", Kind: "action",
		Prepare: func(s workflow.Step, _ map[string]any) (workflow.Prepared, error) {
			return workflow.Prepared{Park: &workflow.Park{Kind: "test.resumeWork", Key: s.ExecutionID()}}, nil
		},
		Resume: func(_ workflow.Step, c workflow.Completion) (workflow.Prepared, error) {
			raw, err := json.Marshal(c.Output)
			return workflow.Prepared{Job: raw}, err
		},
		Work: func(_ context.Context, _ workflow.Tools, raw json.RawMessage) (json.RawMessage, error) {
			var out map[string]any
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
			out["processed"] = true
			return json.Marshal(out)
		},
		Commit: func(_ workflow.Step, _ json.RawMessage, raw json.RawMessage) (workflow.Prepared, error) {
			var out map[string]any
			err := json.Unmarshal(raw, &out)
			return workflow.Prepared{Output: out}, err
		},
	})
}

func TestResumedActionCanRunWorkAndCommit(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	w := f.workflow(t, "Resume work", `{"triggers":[{"type":"manual"}],"flow":{"start":"ask","nodes":{"ask":{"activity":"test.resumeWork","next":{"done":"save"}},"save":{"activity":"set_variable","inputs":{"name":"result","value":"{step:ask.processed}"}}}}}`)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	page, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.resumeWork", WorkspaceID: f.ws.ID})
	if err != nil || len(page.Data) != 1 {
		t.Fatal(page, err)
	}
	if err = f.wf.FinishWait(ctx, "alice", f.ws.ID, "test.resumeWork", page.Data[0].Key, workflow.Completion{Output: map[string]any{"value": "ready"}}); err != nil {
		t.Fatal(err)
	}
	run := f.wait(t, ids[0], settled)
	if run.Status != "completed" {
		t.Fatalf("resume failed: %s %+v", run.Error, run.Steps)
	}
	vars, _ := run.Result.(map[string]any)["variables"].(map[string]any)
	if vars["result"] != true {
		t.Fatalf("resume work never committed: %+v", vars)
	}
}
func TestCycleCreatesNewWaitForEachVisit(t *testing.T) {
	f := setup(t)
	f.searchOff(t)
	item := f.item(t, "alice", "Cycle", nil)
	w := f.workflow(t, "Cycle", `{"triggers":[{"type":"manual","collection_id":"$LIST"}],"variables":{"round":"first"},"flow":{"start":"ask","nodes":{"ask":{"activity":"test.cyclewait","next":{"done":"check"}},"check":{"activity":"if","inputs":{"left":"{var:round}","op":"eq","right":"first"},"next":{"true":"advance","false":"end"}},"advance":{"activity":"set_variable","inputs":{"name":"round","value":"second"},"next":{"done":"ask"}},"end":{"activity":"end"}}}}`)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	page, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.cyclewait", WorkspaceID: f.ws.ID})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("waits %+v %v", page, err)
	}
	if err = f.wf.FinishWait(ctx, "alice", f.ws.ID, "test.cyclewait", page.Data[0].Key, workflow.Completion{Output: map[string]any{"answer": "first"}}); err != nil {
		t.Fatal(err)
	}
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		run, err := f.r.Run(ctx, "alice", ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == "completed" {
			t.Fatal("second visit reused first completion and finished without waiting")
		}
		if run.Status == "failed" {
			t.Fatalf("second wait failed instead of waiting: %s", run.Error)
		}
		next, err := f.wf.ListWaits(ctx, "alice", workflow.WaitQuery{Kind: "test.cyclewait", WorkspaceID: f.ws.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Data) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no new wait was created for the second visit")
}
