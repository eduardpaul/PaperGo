package runner

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/model"
	"papergo/internal/tasks"
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
		"yes": {"activity": "item.update", "inputs": {"values": {"note": "approved by {step:ask.completed_by}: {step:ask.input.reason}"}}},
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
	mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"})
	if err != nil || len(mine) != 1 {
		t.Fatalf("bob's approvals: %v, %v", mine, err)
	}
	a := mine[0]
	if a.Title != "Review Contract" || a.RunID == nil || *a.RunID != ids[0] || a.ItemID == nil || *a.ItemID != item.ID {
		t.Fatalf("approval %+v", a)
	}
	// Only assignees decide; the form is checked.
	if _, err = f.tasks().Respond(ctx, "carol", a.ID, tasks.Response{Outcome: "approved"}); err == nil {
		t.Fatal("a stranger decided")
	}
	if _, err = f.tasks().Respond(ctx, "bob", a.ID, tasks.Response{Outcome: "approved"}); err == nil {
		t.Fatal("approved without the required reason")
	}
	if _, err = f.tasks().Respond(ctx, "bob", a.ID, tasks.Response{Outcome: "approved", Input: map[string]any{"reason": "fine"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", a.ID, tasks.Response{Outcome: "rejected", Comment: "not now"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("decided twice: %v", err)
	}
	run := f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "approved by bob: fine" {
		t.Fatalf("note %v; run %+v", got, run)
	}
	if got, _ := f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"}); len(got) != 0 {
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
	all, err := f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{})
	if err != nil || len(all) != 1 || all[0].Status != "cancelled" {
		t.Fatalf("approvals %v, %v", all, err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", all[0].ID, tasks.Response{Outcome: "rejected", Comment: "not now"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("decided a cancelled request: %v", err)
	}
}

// A run cancelled after its task completed, before or while it resumes,
// stays cancelled and runs nothing after the task node.
func TestCancellingARunWhoseTaskCompletedStopsIt(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Review", approvalFlow)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"})
	if err != nil || len(mine) != 1 {
		t.Fatalf("bob's tasks: %v, %v", mine, err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", mine[0].ID, tasks.Response{Outcome: "approved", Input: map[string]any{"reason": "fine"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Cancel(ctx, "alice", ids[0]); err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "cancelled")
	// Give a resume that was under way time to run on.
	time.Sleep(500 * time.Millisecond)
	f.status(t, ids[0], "cancelled")
	if got := f.head(t, item.ID).Values["note"]; got != nil {
		t.Fatalf("the cancelled run went on: note %v", got)
	}
	if parked, err := parkedRuns(ctx, f.dms, ids); err != nil || parked[ids[0]] {
		t.Fatalf("the run is still parked: %v, %v", parked, err)
	}
}

// Parking is not a failure: a task node's error port is not followed and
// nothing is logged as failed.
func TestParkingIsNotAFailure(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Review", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "ask", "nodes": {
			"ask": {"activity": "approval", "inputs": {"title": "Review", "assignees": ["bob"]}, "next": {"approved": "yes", "error": "oops"}},
			"yes": {"activity": "item.update", "inputs": {"values": {"note": "approved"}}},
			"oops": {"activity": "item.update", "inputs": {"values": {"note": "error port"}}}
		}}
	}`)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Status: "open"})
	if err != nil || len(mine) != 1 {
		t.Fatalf("bob's tasks: %v, %v", mine, err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", mine[0].ID, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	run := f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "approved" {
		t.Fatalf("note %v", got)
	}
	for _, s := range run.Steps {
		if strings.HasPrefix(s.Name, "oops") {
			t.Fatalf("the error port ran: %+v", run.Steps)
		}
	}
	if logs := f.logs.String(); strings.Contains(logs, "workflow run failed") {
		t.Fatalf("parking logged a failure:\n%s", logs)
	}
}

// Deleting the item a run works on cancels the run and the task it waits for.
func TestDeletingTheItemCancelsAWaitingRun(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Review", approvalFlow)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	if err = f.dms.Delete(ctx, "alice", item.ID, f.head(t, item.ID).Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "cancelled")
	all, err := f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{})
	if err != nil || len(all) != 1 || all[0].Status != "cancelled" {
		t.Fatalf("tasks %v, %v", all, err)
	}
}

// Retention keeps parked runs and still deletes the finished runs after them.
func TestPurgeSkipsParkedRuns(t *testing.T) {
	f := setup(t)
	defer func(n int) { purgePage = n }(purgePage)
	purgePage = 1
	f.r.cfg.Retention = time.Millisecond
	review := f.workflow(t, "Review", approvalFlow)
	note := f.workflow(t, "Note", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}], "flow": {"start": "n", "nodes": {"n": {"activity": "item.update", "inputs": {"values": {"note": "seen"}}}}}}`)
	item := f.item(t, "alice", "Contract", nil)
	waiting, err := f.wf.StartRuns(ctx, "alice", review.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, waiting[0], "waiting")
	done, err := f.wf.StartRuns(ctx, "alice", note.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, done[0], "completed")
	time.Sleep(10 * time.Millisecond)
	if n, err := f.r.Purge(ctx); err != nil || n != 1 {
		t.Fatalf("purged %d, %v", n, err)
	}
	f.status(t, waiting[0], "waiting")
	if _, err = f.r.Run(ctx, "alice", done[0]); !errors.Is(err, dms.ErrNotFound) {
		t.Fatalf("the finished run is kept: %v", err)
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

const invoiceCheck = `{
	"triggers": [{"type": "manual", "collection_id": "$LIST"}],
	"flow": {"start": "check", "nodes": {
		"check": {"activity": "task", "inputs": {"title": "Check {item:name}", "assignees": ["bob"], "key": "invoice_check",
			"outcomes": [
				{"key": "correct", "label": "Correct", "style": "primary"},
				{"key": "fix", "label": "Needs a fix", "comment": "required", "skip_form": true},
				{"key": "not_invoice", "label": "Not an invoice", "style": "danger", "skip_form": true}],
			"input_schema": {"type": "object", "required": ["total"], "properties": {"total": {"type": "integer", "title": "Total"}, "note": {"type": "string"}}},
			"values": {"total": "{item:values.total}"}},
			"next": {"correct": "save", "fix": "flag"}},
		"save": {"activity": "item.update", "inputs": {"values": {"total": "{step:check.input.total}", "note": "checked by {step:check.completed_by}"}}},
		"flag": {"activity": "item.update", "inputs": {"values": {"note": "fix: {step:check.comment}"}}}
	}}
}`

func TestCustomTaskOutcomesFormsAndPrefill(t *testing.T) {
	f := setup(t)
	w := f.workflow(t, "Invoice check", invoiceCheck)
	start := func(name string) (string, string) {
		t.Helper()
		item := f.item(t, "alice", name, map[string]any{"total": json.Number("120")})
		ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
		if err != nil {
			t.Fatal(err)
		}
		f.status(t, ids[0], "waiting")
		mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Waiting: true, ItemID: item.ID})
		if err != nil || len(mine) != 1 {
			t.Fatalf("bob's tasks: %v %v", mine, err)
		}
		return ids[0], mine[0].ID
	}

	run, id := start("Invoice 1")
	tk, err := f.tasks().Get(ctx, "bob", id)
	if err != nil {
		t.Fatal(err)
	}
	// The form is frozen on the task and prefilled from the item.
	if tk.Key != "invoice_check" || tk.Kind != "task" || len(tk.Outcomes) != 3 || tk.Values["total"] != 120.0 || tk.InputSchema == nil || !tk.CanRespond {
		t.Fatalf("task %+v", tk)
	}
	for name, r := range map[string]tasks.Response{
		"unknown outcome":    {Outcome: "maybe"},
		"missing form value": {Outcome: "correct"},
		"form type":          {Outcome: "correct", Input: map[string]any{"total": "many"}},
		"missing comment":    {Outcome: "fix"},
		"form on skip":       {Outcome: "not_invoice", Input: map[string]any{"total": 1}},
	} {
		if _, err = f.tasks().Respond(ctx, "bob", id, r); err == nil {
			t.Errorf("%s is accepted", name)
		}
	}
	if _, err = f.tasks().Respond(ctx, "carol", id, tasks.Response{Outcome: "correct", Input: map[string]any{"total": 1}}); !errors.Is(err, dms.ErrNotFound) {
		t.Errorf("a stranger answered: %v", err)
	}
	// Nobody else sees it: not in their lists, not by ID.
	if _, err = f.tasks().Get(ctx, "carol", id); !errors.Is(err, dms.ErrNotFound) {
		t.Errorf("a stranger read the task: %v", err)
	}
	if theirs, _ := f.tasks().Mine(ctx, "carol", tasks.Filter{}); len(theirs) != 0 {
		t.Errorf("a stranger lists %v", theirs)
	}
	if _, err = f.tasks().Workspace(ctx, "bob", f.ws.ID, tasks.Filter{}); !errors.Is(err, dms.ErrForbidden) {
		t.Errorf("a member listed the workspace's tasks: %v", err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "correct", Input: map[string]any{"total": 125}}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	items, _ := f.tasks().Get(ctx, "bob", id)
	head := f.head(t, *items.ItemID)
	if head.Values["total"] != json.Number("125") && head.Values["total"] != 125.0 && head.Values["total"] != int64(125) || head.Values["note"] != "checked by bob" {
		t.Fatalf("values %v", head.Values)
	}

	// An outcome that skips the form, with a required comment.
	run, id = start("Invoice 2")
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "fix", Comment: "VAT missing"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	done, _ := f.tasks().Get(ctx, "bob", id)
	if done.Status != "completed" || *done.Outcome != "fix" || f.head(t, *done.ItemID).Values["note"] != "fix: VAT missing" || len(done.Responses) != 1 {
		t.Fatalf("task %+v", done)
	}
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "correct", Input: map[string]any{"total": 1}}); !errors.Is(err, dms.ErrConflict) {
		t.Errorf("answered a completed task: %v", err)
	}

	// An outcome with no port ends the run.
	run, id = start("Invoice 3")
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "not_invoice"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
}

func TestAllModeTaskWithGroupsAndDecisiveOutcome(t *testing.T) {
	f := setup(t)
	if err := f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "carol", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	group, err := f.dms.Directory().SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Legal"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.dms.Directory().SetMember(ctx, "alice", group.ID, identity.Principal{Type: "user", ID: "carol"}, false); err != nil {
		t.Fatal(err)
	}
	w := f.workflow(t, "Publish review", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "review", "nodes": {
			"review": {"activity": "approval", "inputs": {"title": "Publish {item:name}?", "assignees": ["bob", "group:`+group.ID+`"], "completion": "all"},
				"next": {"approved": "yes", "rejected": "no"}},
			"yes": {"activity": "item.update", "inputs": {"values": {"note": "approved by all"}}},
			"no": {"activity": "item.update", "inputs": {"values": {"note": "rejected: {step:review.comment}"}}}
		}}}`)
	start := func(name string) (string, string, string) {
		t.Helper()
		item := f.item(t, "alice", name, nil)
		ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
		if err != nil {
			t.Fatal(err)
		}
		f.status(t, ids[0], "waiting")
		// carol sees the group's task.
		mine, err := f.tasks().Mine(ctx, "carol", tasks.Filter{Waiting: true, ItemID: item.ID})
		if err != nil || len(mine) != 1 || mine[0].Kind != "approval" {
			t.Fatalf("carol's tasks: %v %v", mine, err)
		}
		return ids[0], mine[0].ID, item.ID
	}

	// Everyone approves: the group answers once, through carol.
	run, id, item := start("Policy")
	if _, err = f.tasks().Respond(ctx, "carol", id, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.tasks().Respond(ctx, "carol", id, tasks.Response{Outcome: "approved"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("the group answered twice: %v", err)
	}
	if mine, _ := f.tasks().Mine(ctx, "carol", tasks.Filter{Waiting: true}); len(mine) != 0 {
		t.Fatalf("an answered task still waits for carol: %v", mine)
	}
	f.status(t, run, "waiting")
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	if got := f.head(t, item).Values["note"]; got != "approved by all" {
		t.Fatalf("note %v", got)
	}
	done, err := f.tasks().Get(ctx, "alice", id)
	if err != nil || len(done.Responses) != 2 || done.Responses[0].Assignee != "group:"+group.ID {
		t.Fatalf("responses %+v %v", done.Responses, err)
	}

	// A rejection is decisive: it ends the task at once.
	run, id, item = start("Contract")
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "rejected", Comment: "not yet"}); err != nil {
		t.Fatal(err)
	}
	f.status(t, run, "completed")
	if got := f.head(t, item).Values["note"]; got != "rejected: not yet" {
		t.Fatalf("note %v", got)
	}
}

// A person assigned by name and through a group answers once for each.
func TestAnsweringForEachEntryOfAnAssignee(t *testing.T) {
	f := setup(t)
	if err := f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "carol", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	group, err := f.dms.Directory().SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Legal"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.dms.Directory().SetMember(ctx, "alice", group.ID, identity.Principal{Type: "user", ID: "carol"}, false); err != nil {
		t.Fatal(err)
	}
	tk, err := f.tasks().Create(ctx, "alice", tasks.Input{WorkspaceID: f.ws.ID, Title: "Sign off", Assignees: []string{"carol", "group:" + group.ID}, Completion: tasks.CompletionAll})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.tasks().Respond(ctx, "carol", tk.ID, tasks.Response{Outcome: "done"}); err != nil || v.Status != "open" || !v.CanRespond {
		t.Fatalf("after carol's first answer: %+v, %v", v, err)
	}
	if mine, _ := f.tasks().Mine(ctx, "carol", tasks.Filter{Waiting: true}); len(mine) != 1 {
		t.Fatalf("the task no longer waits for carol's group: %v", mine)
	}
	v, err := f.tasks().Respond(ctx, "carol", tk.ID, tasks.Response{Outcome: "done"})
	if err != nil || v.Status != "completed" || len(v.Responses) != 2 || v.Responses[1].Assignee != "group:"+group.ID || v.CanRespond || !v.Answered {
		t.Fatalf("after carol's second answer: %+v, %v", v, err)
	}
	if _, err = f.tasks().Respond(ctx, "carol", tk.ID, tasks.Response{Outcome: "done"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("answered a third time: %v", err)
	}
}

// With completion all, the result's comment, input and completer come from
// the latest answer with the outcome chosen most often.
func TestAllModeResultComesFromTheWinningAnswer(t *testing.T) {
	f := setup(t)
	if err := f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "carol", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	tk, err := f.tasks().Create(ctx, "alice", tasks.Input{WorkspaceID: f.ws.ID, Title: "Count", Assignees: []string{"alice", "bob", "carol"}, Completion: tasks.CompletionAll,
		Outcomes:    []model.TaskOutcome{{Key: "ok", Label: "OK", Comment: "optional"}, {Key: "fix", Label: "Fix", Comment: "optional"}},
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "string"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct{ who, outcome string }{{"alice", "ok"}, {"bob", "ok"}, {"carol", "fix"}} {
		if _, err = f.tasks().Respond(ctx, a.who, tk.ID, tasks.Response{Outcome: a.outcome, Comment: a.who, Input: map[string]any{"n": a.who}}); err != nil {
			t.Fatal(err)
		}
	}
	v, err := f.tasks().Get(ctx, "alice", tk.ID)
	if err != nil || v.Status != "completed" || *v.Outcome != "ok" || v.Comment != "bob" || *v.CompletedBy != "bob" || !strings.Contains(string(v.Input), `"bob"`) {
		t.Fatalf("result %+v %s, %v", v, v.Input, err)
	}
}

// An assignee who can no longer answer leaves an all-mode task waiting; an
// owner reassigns it, and the task completes once everyone left answered.
func TestReassigningATaskSomeoneCannotAnswer(t *testing.T) {
	f := setup(t)
	if err := f.dms.SetWorkspaceMember(ctx, "alice", f.ws.ID, dms.WorkspaceMember{Subject: "carol", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	w := f.workflow(t, "Sign-off", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "ask", "nodes": {
			"ask": {"activity": "approval", "inputs": {"title": "Sign {item:name}", "assignees": ["bob", "carol"], "completion": "all"}, "next": {"approved": "yes"}},
			"yes": {"activity": "item.update", "inputs": {"values": {"note": "signed by {step:ask.completed_by}"}}}
		}}
	}`)
	item := f.item(t, "alice", "Lease", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Waiting: true})
	if err != nil || len(mine) != 1 {
		t.Fatalf("bob's tasks: %v, %v", mine, err)
	}
	id := mine[0].ID
	if _, err = f.tasks().Respond(ctx, "bob", id, tasks.Response{Outcome: "approved"}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.Directory().SetUserActive(ctx, "organization-admin", "carol", false); err != nil {
		t.Fatal(err)
	}
	// Only owners and the creator of a person's task reassign.
	if _, err = f.tasks().Reassign(ctx, "bob", id, []string{"bob"}); !errors.Is(err, dms.ErrForbidden) {
		t.Fatalf("an assignee reassigned: %v", err)
	}
	if _, err = f.tasks().Reassign(ctx, "alice", id, []string{"bob", "nobody"}); err == nil {
		t.Fatal("reassigned to an unknown person")
	}
	v, err := f.tasks().Reassign(ctx, "alice", id, []string{"bob"})
	if err != nil || v.Status != "completed" || *v.Outcome != "approved" || *v.CompletedBy != "bob" {
		t.Fatalf("reassigned: %+v, %v", v, err)
	}
	f.status(t, ids[0], "completed")
	if got := f.head(t, item.ID).Values["note"]; got != "signed by bob" {
		t.Fatalf("note %v", got)
	}
	if _, err = f.tasks().Reassign(ctx, "alice", id, []string{"carol"}); !errors.Is(err, dms.ErrConflict) {
		t.Fatalf("reassigned a completed task: %v", err)
	}
}

func TestTaskWithoutWaitingAndTaskTriggers(t *testing.T) {
	f := setup(t)
	follow := f.workflow(t, "File it", `{
		"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "ask", "nodes": {"ask": {"activity": "task", "inputs": {"title": "File the signed copy of {item:name}", "assignees": ["bob"], "key": "file_copy", "wait": false}}}}}`)
	react := f.workflow(t, "Filed", `{
		"triggers": [{"type": "task.completed", "task_key": "file_copy"}],
		"flow": {"start": "note", "nodes": {"note": {"activity": "item.update", "inputs": {"values": {"note": "filed by {trigger:data.completed_by}"}}}}}}`)
	other := f.workflow(t, "Other key", `{
		"triggers": [{"type": "task.completed", "task_key": "something_else"}],
		"flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	item := f.item(t, "alice", "Lease", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", follow.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// The run does not wait; the task stays open.
	f.status(t, ids[0], "completed")
	mine, err := f.tasks().Mine(ctx, "bob", tasks.Filter{Waiting: true})
	if err != nil || len(mine) != 1 || mine[0].Outcomes[0].Key != "done" {
		t.Fatalf("bob's tasks: %v %v", mine, err)
	}
	if _, err = f.tasks().Respond(ctx, "bob", mine[0].ID, tasks.Response{Outcome: "done"}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, react.ID, 1)
	if runs[0].Status != "completed" || f.head(t, item.ID).Values["note"] != "filed by bob" {
		t.Fatalf("reaction %+v, note %v", runs[0], f.head(t, item.ID).Values["note"])
	}
	f.runs(t, other.ID, 0)
}

func TestTaskNodeValidationAndRecipients(t *testing.T) {
	f := setup(t)
	for name, c := range map[string]struct{ def, err string }{
		"port not an outcome": {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "outcomes": [{"key": "yes", "label": "Yes"}]}, "next": {"no": "b"}}, "b": {"activity": "end"}}}}`, "no port no"},
		"outcomes as token":   {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "outcomes": "{var:o}"}}}}}`, "literal list"},
		"reserved outcome":    {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "outcomes": [{"key": "expired", "label": "E"}]}}}}}`, "other than error and expired"},
		"bad form":            {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "input_schema": {"type": "string"}}}}}}`, "must describe an object"},
		"ports without wait":  {`{"triggers": [{"type": "manual"}], "flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "wait": false}, "next": {"done": "b", "x": "b"}}, "b": {"activity": "end"}}}}`, "no port x"},
		"task key on item":    {`{"triggers": [{"type": "item.created", "task_key": "x"}], "flow": {"start": "a", "nodes": {"a": {"activity": "end"}}}}`, "task_key"},
	} {
		if _, err := f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: name, Definition: json.RawMessage(c.def)}); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v, want %q", name, err, c.err)
		}
	}
	// Unknown recipients fail the node with their names.
	w := f.workflow(t, "Unknown", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "assignees": ["bob", "nobody", "group:missing"]}}}}}`)
	item := f.item(t, "alice", "Lease", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.status(t, ids[0], "failed"); !strings.Contains(run.Error, "unknown recipients: group:missing, nobody") {
		t.Fatalf("run %+v", run)
	}
	// The item's creator and a person field resolve; cancelling closes the task.
	w = f.workflow(t, "Creator", `{"triggers": [{"type": "manual", "collection_id": "$LIST"}],
		"flow": {"start": "a", "nodes": {
			"a": {"activity": "task", "inputs": {"title": "Look at {item:name}", "assignees": ["creator"], "wait": false}, "next": {"done": "b"}},
			"b": {"activity": "task.cancel", "inputs": {"task_id": "{step:a.task_id}"}}}}}`)
	bobs := f.item(t, "bob", "Bob's lease", nil)
	ids, err = f.wf.StartRuns(ctx, "alice", w.ID, workflow.Start{ItemIDs: []string{bobs.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "completed")
	all, err := f.tasks().Item(ctx, "alice", bobs.ID, tasks.Filter{})
	if err != nil || len(all) != 1 || all[0].Assignees[0] != "bob" || all[0].Status != "cancelled" {
		t.Fatalf("tasks %+v %v", all, err)
	}
}

// A task form's pickers must point at this workspace, as a launch form's
// do, and prefilled picks must be valid answers.
func TestTaskFormsCheckTheirPickers(t *testing.T) {
	f := setup(t)
	other, err := f.dms.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, err := f.dms.Create(ctx, "alice", other.ID, dms.CreateResource{Kind: "list", Name: "Elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.dms.Create(ctx, "alice", elsewhere.ID, dms.CreateResource{Kind: "item", Name: "Foreign"})
	if err != nil {
		t.Fatal(err)
	}
	picker := `{"type": "object", "properties": {"doc": {"type": "string", "x-papergo": {"kind": "item", "collection_id": "` + elsewhere.ID + `"}}}}`
	if _, err = f.wf.Create(ctx, "alice", f.ws.ID, workflow.Save{Name: "Pick", Definition: json.RawMessage(`{"triggers": [{"type": "manual"}],
		"flow": {"start": "a", "nodes": {"a": {"activity": "task", "inputs": {"title": "x", "input_schema": ` + picker + `}}}}}`)}); err == nil || !strings.Contains(err.Error(), "collection_id") {
		t.Errorf("a task form picking from another workspace: %v", err)
	}
	var schema map[string]any
	if err = json.Unmarshal([]byte(picker), &schema); err != nil {
		t.Fatal(err)
	}
	if _, err = f.tasks().Create(ctx, "alice", tasks.Input{WorkspaceID: f.ws.ID, Title: "x", Assignees: []string{"bob"}, InputSchema: schema}); err == nil {
		t.Error("a user task form picking from another workspace")
	}
	open := map[string]any{"type": "object", "properties": map[string]any{"doc": map[string]any{"type": "string", "x-papergo": map[string]any{"kind": "item"}}}}
	if _, err = f.tasks().Create(ctx, "alice", tasks.Input{WorkspaceID: f.ws.ID, Title: "x", Assignees: []string{"bob"}, InputSchema: open, Values: map[string]any{"doc": foreign.ID}}); err == nil {
		t.Error("a prefilled pick from another workspace")
	}
}

func TestTaskCancelRefusesATaskARunWaitsFor(t *testing.T) {
	f := setup(t)
	waiting := f.workflow(t, "Waits", approvalFlow)
	item := f.item(t, "alice", "Contract", nil)
	ids, err := f.wf.StartRuns(ctx, "alice", waiting.ID, workflow.Start{ItemIDs: []string{item.ID}})
	if err != nil {
		t.Fatal(err)
	}
	f.status(t, ids[0], "waiting")
	open, err := f.tasks().Workspace(ctx, "alice", f.ws.ID, tasks.Filter{Status: "open"})
	if err != nil || len(open) != 1 {
		t.Fatalf("open tasks %v %v", open, err)
	}
	cancel := f.workflow(t, "Cancel", `{"triggers": [{"type": "manual"}], "flow": {"start": "c", "nodes": {"c": {"activity": "task.cancel", "inputs": {"task_id": "`+open[0].ID+`"}}}}}`)
	runs, err := f.wf.StartRuns(ctx, "alice", cancel.ID, workflow.Start{})
	if err != nil {
		t.Fatal(err)
	}
	if run := f.status(t, runs[0], "failed"); !strings.Contains(run.Error, "cancel the run instead") {
		t.Fatalf("run %+v", run)
	}
	f.status(t, ids[0], "waiting")
}
