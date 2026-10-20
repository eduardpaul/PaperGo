package tasks_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"sync"
	"testing"
	"time"

	"papergo/internal/dal"
	"papergo/internal/dal/dberr"
	runstore "papergo/internal/dal/runs"
	taskstore "papergo/internal/dal/tasks"
	"papergo/internal/dal/workflows"
)

func runDALTaskSuite(t *testing.T, newStore func(testing.TB) *dal.Store) {
	t.Run("TaskWorkflowContainment", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		ws, err := s.Resources().Create(ctx, resources.New{ID: "other-workspace", Kind: refs.KindWorkspace, Name: "Other"})
		if err != nil {
			t.Fatal(err)
		}
		workflow, err := s.Workflows().Create(ctx, workflows.CreateInput{ID: "workflow", WorkspaceID: ws.ID, Key: "review", Name: "Review", CreatedBy: "alice"})
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "run", WorkflowID: workflow.ID, WorkspaceID: ws.ID, ItemID: "", EventID: "event", EventType: "manual", Actor: "alice", WorkflowVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		in.Source, in.WorkflowID, in.RunID, in.Node, in.Wait = "workflow", workflow.ID, run.ID, "review", true
		if _, err := s.Tasks().Create(ctx, in); !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("cross-workspace workflow task accepted: %v", err)
		}
		in.WorkspaceID = ws.ID
		created, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if !created.Wait || created.RunID != run.ID {
			t.Fatalf("workflow source lost: %+v", created)
		}
	})

	t.Run("TaskTypedJSONAndFirstAnswer", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		due := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		in.DueAt = &due
		in.InputSchema = map[string]any{"type": "object", "properties": map[string]any{"comment": map[string]any{"type": "string"}}}
		in.Values = map[string]any{"comment": "Prefill"}
		task, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		reloaded, err := s.Tasks().Find(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reloaded.Outcomes[1].AllowStale || !reloaded.Outcomes[1].SkipForm || reloaded.Outcomes[1].Comment != "required" || reloaded.Values["comment"] != "Prefill" || reloaded.DueAt == nil || !reloaded.DueAt.Equal(due) {
			t.Fatalf("task settings lost: %+v", reloaded)
		}
		done, err := s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: "alice", Assignee: "alice", Outcome: "approved", Comment: "Done", Input: []byte(`{"comment":"Complete"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if done.Status != "completed" || done.Outcome != "approved" || done.CompletedBy != "alice" || done.CompletedAt == nil || done.Version != task.Version+1 {
			t.Fatalf("incorrect completion: %+v", done)
		}
		if _, err := s.Tasks().Respond(ctx, task.ID, done.Version, taskstore.ResponseInput{Subject: "bob", Assignee: "bob", Outcome: "rejected"}); !errors.Is(err, dberr.ErrInvalidStateTransition) {
			t.Fatalf("terminal task accepted answer: %v", err)
		}
		responses, err := s.Tasks().Responses(ctx, []string{task.ID}, nil)
		if count := len(responses); err != nil || count != 1 {
			t.Fatalf("terminal answer was not atomic: %d %v", count, err)
		}
	})
	t.Run("TaskAllModeTieReassignmentAndDecisiveAnswer", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		in.Completion = "all"
		task, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		first, err := s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: "bob", Assignee: "bob", Outcome: "rejected", Comment: "No"})
		if err != nil {
			t.Fatal(err)
		}
		if first.Status != "open" {
			t.Fatal("all-mode task completed before everyone answered")
		}
		done, err := s.Tasks().Respond(ctx, task.ID, first.Version, taskstore.ResponseInput{Subject: "alice", Assignee: "alice", Outcome: "approved", Comment: "Yes"})
		if err != nil {
			t.Fatal(err)
		}
		if done.Status != "completed" || done.Outcome != "approved" || done.CompletedBy != "alice" || done.Comment != "Yes" {
			t.Fatalf("tie did not choose first configured outcome: %+v", done)
		}
		task, err = s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		first, err = s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: "alice", Assignee: "alice", Outcome: "approved"})
		if err != nil {
			t.Fatal(err)
		}
		done, err = s.Tasks().Reassign(ctx, task.ID, first.Version, []string{"alice"})
		if err != nil {
			t.Fatal(err)
		}
		if done.Status != "completed" || done.Version != first.Version+1 {
			t.Fatalf("reassignment did not complete atomically: %+v", done)
		}
		in.Assignees = []string{"alice", "group:reviewers"}
		in.Outcomes[1].Decisive = true
		task, err = s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		done, err = s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: "carol", Assignee: "group:reviewers", Outcome: "rejected", Comment: "Veto"})
		if err != nil {
			t.Fatal(err)
		}
		if done.Status != "completed" || done.Outcome != "rejected" {
			t.Fatalf("decisive answer did not complete: %+v", done)
		}
	})
	t.Run("TaskAssigneeQueriesAndRollback", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		in.Assignees = []string{"alice", "group:reviewers"}
		in.Completion = "all"
		task, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		find := func(subject string, groups []string, waiting bool) []taskstore.Task {
			t.Helper()
			rows, err := s.Tasks().List(ctx, taskstore.Filter{WorkspaceID: in.WorkspaceID, Status: "open", Subject: subject, Groups: groups, AssignedOnly: true, Waiting: waiting})
			if err != nil {
				t.Fatal(err)
			}
			return rows
		}
		if len(find("bob", nil, false)) != 0 || len(find("carol", []string{"reviewers"}, true)) != 1 || len(find("alice' OR 1=1 --", nil, false)) != 0 {
			t.Fatal("incorrect assignee filtering")
		}
		first, err := s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: "alice", Assignee: "alice", Outcome: "approved"})
		if err != nil {
			t.Fatal(err)
		}
		if len(find("alice", nil, true)) != 0 || len(find("alice", nil, false)) != 1 || len(find("carol", []string{"reviewers"}, true)) != 1 {
			t.Fatal("answered entry not excluded from waiting tasks")
		}
		if _, err := s.Tasks().Reassign(ctx, task.ID, first.Version, []string{"missing"}); !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("unknown assignee accepted: %v", err)
		}
		reloaded, err := s.Tasks().Find(ctx, task.ID)
		if err != nil || reloaded.Version != first.Version {
			t.Fatalf("failed reassignment changed task: %+v %v", reloaded, err)
		}
		tx, err := s.SQLDB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := s.WithTx(tx).Tasks().Respond(ctx, task.ID, first.Version, taskstore.ResponseInput{Subject: "carol", Assignee: "group:reviewers", Outcome: "rejected"}); err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		reloaded, err = s.Tasks().Find(ctx, task.ID)
		if err != nil || reloaded.Status != "open" || reloaded.Version != first.Version {
			t.Fatalf("task completion escaped transaction: %+v %v", reloaded, err)
		}
		responses, err := s.Tasks().Responses(ctx, []string{task.ID}, nil)
		if count := len(responses); err != nil || count != 1 {
			t.Fatalf("response escaped transaction: %d %v", count, err)
		}
	})
	t.Run("ConcurrentTaskResponsesAndCancellation", func(t *testing.T) {
		ctx := context.Background()
		s, in := daltest.TaskFixture(t, newStore)
		task, err := s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, subject := range []string{"alice", "bob"} {
			wg.Go(func() {
				_, errs[i] = s.Tasks().Respond(ctx, task.ID, task.Version, taskstore.ResponseInput{Subject: subject, Assignee: subject, Outcome: "approved"})
			})
		}
		wg.Wait()
		daltest.AssertOneRejected(t, errs, dberr.ErrConcurrentUpdate)
		responses, err := s.Tasks().Responses(ctx, []string{task.ID}, nil)
		if count := len(responses); err != nil || count != 1 {
			t.Fatalf("both concurrent answers recorded: %d %v", count, err)
		}
		task, err = s.Tasks().Create(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		cancelled, err := s.Tasks().Cancel(ctx, task.ID, task.Version)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Status != "cancelled" || cancelled.CompletedAt == nil {
			t.Fatalf("incorrect cancellation: %+v", cancelled)
		}
		if _, err := s.Tasks().Respond(ctx, task.ID, cancelled.Version, taskstore.ResponseInput{Subject: "alice", Assignee: "alice", Outcome: "approved"}); !errors.Is(err, dberr.ErrInvalidStateTransition) {
			t.Fatalf("cancelled task accepted response: %v", err)
		}
	})
}

func TestTasks(t *testing.T) { daltest.Engines(t, runDALTaskSuite) }
