package runs_test

import (
	"context"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"testing"
	"time"
)

func TestWorkflowRunRepository_SQLite(t *testing.T) {
	testWorkflowRunRepository(t, daltest.SQLiteStore(t))
}
func TestWorkflowRunRepository_Postgres(t *testing.T) {
	testWorkflowRunRepository(t, daltest.PostgresStore(t))
}
func testWorkflowRunRepository(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "runs", Name: "Runs", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "first", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-first", EventType: "manual", Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().AddItem(ctx, run.ID, f.Item, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Runs().List(ctx, runstore.Filter{ItemIDs: []string{f.Item}, Statuses: []runstore.Status{runstore.StatusQueued}})
	if err != nil || len(rows) != 1 || rows[0].ID != run.ID {
		t.Fatalf("selection membership not found: %+v %v", rows, err)
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Runs().Observe(ctx, run.ID, "completed", at, true); err != nil {
		t.Fatal(err)
	}
	found, err := s.Runs().Find(ctx, run.ID)
	if err != nil || found.Status != "completed" || !found.StatusAt.Equal(at) {
		t.Fatalf("native timestamp roundtrip: %+v %v", found, err)
	}
	if err := s.Runs().Observe(ctx, run.ID, "running", at.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	found, err = s.Runs().Find(ctx, run.ID)
	if err != nil || found.Status != "completed" {
		t.Fatalf("terminal run reopened: %+v %v", found, err)
	}
	if err := s.Runs().SaveStepResult(ctx, run.ID, 0, "load", []byte(`{"exact":9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().SaveStepResult(ctx, run.ID, 1, "done", []byte(`null`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "retry", WorkflowID: wf.ID, WorkspaceID: f.Workspace, WorkflowVersion: 1, EventID: "retry", EventType: "manual", Actor: "alice", RetryOf: run.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().ForkSteps(ctx, "retry", run.ID, 1); err != nil {
		t.Fatal(err)
	}
	name, body, err := s.Runs().StepResult(ctx, "retry", 0)
	if err != nil || name != "load" || string(body) != `{"exact":9007199254740993}` {
		t.Fatalf("forked body changed: %q %s %v", name, body, err)
	}
	if _, _, err := s.Runs().StepResult(ctx, "retry", 1); !dberr.IsNotFound(err) {
		t.Fatalf("fork copied unreused step: %v", err)
	}
	if err := s.Runs().DeleteSteps(ctx, []string{"retry"}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.Runs().StepBodies(ctx, "retry"); err != nil || len(rows) != 0 {
		t.Fatalf("step bodies retained: %+v %v", rows, err)
	}
}
