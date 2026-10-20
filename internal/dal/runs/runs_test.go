package runs_test

import (
	"context"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
	"papergo/internal/jsonutil"
	"strings"
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
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "runs", Name: "Runs", Content: daltest.Script, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Runs().Create(ctx, runstore.CreateInput{ID: "orphan", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-orphan", EventType: "manual", Actor: "alice"}); err == nil {
		t.Fatal("a run without its operation was recorded")
	}
	if err := daltest.Run(ctx, s, runstore.CreateInput{ID: "first", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-first", EventType: "manual", Actor: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().AddItem(ctx, "first", f.Item, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Runs().List(ctx, runstore.Filter{ItemIDs: []string{f.Item}, Statuses: []runstore.Status{runstore.StatusQueued}})
	if err != nil || len(rows) != 1 || rows[0].ID != "first" {
		t.Fatalf("selection membership not found: %+v %v", rows, err)
	}
	if err := s.Runs().AddLogs(ctx, "first", []runstore.Log{{Level: "info", Message: "started", At: time.Now().UTC()}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().RecordSteps(ctx, []runstore.Step{
		{RunID: "first", Seq: 0, Kind: runstore.StepRequest, Name: "GET /v1/items", InputHash: strings.Repeat("a", 64), Status: runstore.StepCompleted, Output: []byte(`{"exact":9007199254740993}`)},
		{RunID: "first", Seq: 1, Kind: runstore.StepValue, Name: "now", InputHash: strings.Repeat("b", 64), Status: runstore.StepCompleted, Output: []byte(`1`)},
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{daltest.RunKind}, IDs: []string{"first"}, Now: time.Now(), Limit: 1}, Holder: "test", TTL: time.Minute})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim the run: %+v %v", claimed, err)
	}
	op := claimed[0]
	op.Status, op.Result = operations.StatusSucceeded, []byte(`{"ok":true}`)
	if err := s.Operations().Finish(ctx, &op, time.Now()); err != nil {
		t.Fatal(err)
	}
	found, err := s.Runs().Find(ctx, "first")
	if err != nil || found.Status != runstore.StatusCompleted || !jsonutil.Equal(found.Output, []byte(`{"ok":true}`)) || len(found.Logs) != 1 || found.CompletedAt == nil {
		t.Fatalf("run does not follow its operation: %+v %v", found, err)
	}
	if err := daltest.Run(ctx, s, runstore.CreateInput{ID: "retry", WorkflowID: wf.ID, WorkspaceID: f.Workspace, WorkflowVersion: 1, EventID: "retry", EventType: "manual", Actor: "alice", RetryOf: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().ForkSteps(ctx, "retry", "first", 1); err != nil {
		t.Fatal(err)
	}
	steps, err := s.Runs().Steps(ctx, "retry")
	if err != nil || len(steps) != 1 || steps[0].Name != "GET /v1/items" || !jsonutil.Equal(steps[0].Output, []byte(`{"exact":9007199254740993}`)) {
		t.Fatalf("fork copied the wrong steps: %+v %v", steps, err)
	}
	if err := s.Operations().Delete(ctx, []string{"retry"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Runs().Find(ctx, "retry"); !dberr.IsNotFound(err) {
		t.Fatalf("run outlived its operation: %v", err)
	}
	if steps, err := s.Runs().Steps(ctx, "retry"); err != nil || len(steps) != 0 {
		t.Fatalf("steps outlived their run: %+v %v", steps, err)
	}
}
