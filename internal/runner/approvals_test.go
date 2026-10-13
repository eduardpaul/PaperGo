package runner

import (
	"errors"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// status waits until run id has the status.
func (f *fixture) status(t *testing.T, id, want string) Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		run, err := f.r.Run(ctx, "alice", id)
		if err == nil && run.Status == want {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s is %s (%v), want %s", id, run.Status, err, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const approvalFlow = `{
	"triggers": [{"type": "manual", "collection_id": "$LIST"}],
	"flow": {"start": "ask", "nodes": {
		"ask": {"activity": "approval", "inputs": {"title": "Review {item:name}", "assignees": ["bob"],
			"input_schema": {"type": "object", "required": ["reason"], "properties": {"reason": {"type": "string"}}}},
			"next": {"approved": "yes", "rejected": "no"}},
		"yes": {"activity": "item.update", "inputs": {"values": {"note": "approved by {step:ask.decided_by}: {step:ask.input.reason}"}}},
		"no": {"activity": "item.update", "inputs": {"values": {"note": "rejected"}}}
	}}
}`

func TestApprovalParksAndResumes(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Review", approvalFlow)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	mine, err := f.wf.MyApprovals(ctx, "bob", workflow.ApprovalFilter{Status: "open"})
	if err != nil || len(mine) != 1 {
		t.Fatalf("bob's approvals: %v, %v", mine, err)
	}
	a := mine[0]
	if a.Title != "Review Contract" || a.RunID != ids[0] || a.ItemID == nil || *a.ItemID != item.ID {
		t.Fatalf("approval %+v", a)
	}
	// Only assignees decide; the form is checked.
	if _, err = f.wf.Decide(ctx, "carol", a.ID, workflow.Decision{Decision: "approve"}); err == nil {
		t.Fatal("a stranger decided")
	}
	if _, err = f.wf.Decide(ctx, "bob", a.ID, workflow.Decision{Decision: "approve"}); err == nil {
		t.Fatal("approved without the required reason")
	}
	if _, err = f.wf.Decide(ctx, "bob", a.ID, workflow.Decision{Decision: "approve", Input: map[string]any{"reason": "fine"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.wf.Decide(ctx, "bob", a.ID, workflow.Decision{Decision: "reject"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("decided twice: %v", err)
	}
	run := f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "approved by bob: fine" {
		t.Fatalf("note %v; run %+v", got, run)
	}
	if got, _ := f.wf.MyApprovals(ctx, "bob", workflow.ApprovalFilter{Status: "open"}); len(got) != 0 {
		t.Fatalf("open approvals after the decision: %v", got)
	}
}

func TestCancellingAWaitingRunClosesItsApproval(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Review", approvalFlow)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	if _, err = f.r.Cancel(ctx, "alice", ids[0]); err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "cancelled")
	all, err := f.wf.WorkspaceApprovals(ctx, "alice", f.ws.ID, workflow.ApprovalFilter{})
	if err != nil || len(all) != 1 || all[0].Status != "cancelled" {
		t.Fatalf("approvals %v, %v", all, err)
	}
	if _, err = f.wf.Decide(ctx, "bob", all[0].ID, workflow.Decision{Decision: "reject"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("decided a cancelled request: %v", err)
	}
}

func TestWaitingRunsHoldNoWorker(t *testing.T) {
	f := setup(t)
	review := f.workflow(t, "Review", approvalFlow)
	// More waiting runs than the queue runs at once (4).
	var waiting []string
	for i := range 6 {
		item := f.item(t, "alice", "Contract "+string(rune('A'+i)), nil)
		ids, err := f.wf.StartRuns(ctx, "alice", review.ID, workflow.Start{ItemIDs: []string{item.ID}})
		if err != nil {
			t.Fatal(err)
		}
		waiting = append(waiting, ids[0])
	}
	for _, id := range waiting {
		f.status(t, id, "waiting")
	}
	quick := f.workflow(t, "Quick", `{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`)
	ids, err := f.wf.StartRuns(ctx, "alice", quick.ID, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
}
