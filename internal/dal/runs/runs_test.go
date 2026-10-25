package runs_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

func TestStepJournal_SQLite(t *testing.T)   { testStepJournal(t, daltest.SQLiteStore(t)) }
func TestStepJournal_Postgres(t *testing.T) { testStepJournal(t, daltest.PostgresStore(t)) }

// A journal stores outputs compressed, releases a wait once all its
// operations ended, and keeps only small outputs once its run is over.
func testStepJournal(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "journal", Name: "Journal", Content: daltest.Script, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run", "op-a", "op-b"} {
		if err := daltest.Run(ctx, s, runstore.CreateInput{ID: id, WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-" + id, EventType: "manual", Actor: "alice"}); err != nil {
			t.Fatal(err)
		}
	}
	random := make([]byte, 16<<10)
	_, _ = rand.Read(random)
	outputs := []json.RawMessage{
		json.RawMessage(`{"body":"` + strings.Repeat("compressible ", 5000) + `"}`),
		json.RawMessage(`{"body":"` + base64.StdEncoding.EncodeToString(random) + `"}`),
		json.RawMessage(`{"status":200}`),
	}
	var steps []runstore.Step
	for i, output := range outputs {
		steps = append(steps, runstore.Step{RunID: "run", Seq: i, Kind: runstore.StepRequest, Name: "GET /v1/items", InputHash: strings.Repeat("a", 64), Status: runstore.StepCompleted, Output: output})
	}
	steps = append(steps, runstore.Step{RunID: "run", Seq: 3, Kind: runstore.StepWait, Name: "operations", InputHash: strings.Repeat("b", 64), Status: runstore.StepPending, Operations: []string{"op-b", "op-a"}})
	if err := s.Runs().RecordSteps(ctx, steps); err != nil {
		t.Fatal(err)
	}
	read, err := s.Runs().Steps(ctx, "run")
	if err != nil || len(read) != 4 {
		t.Fatalf("steps: %+v %v", read, err)
	}
	for i, output := range outputs {
		if string(read[i].Output) != string(output) || read[i].OutputSize != len(output) {
			t.Fatalf("step %d did not read back as recorded: %d bytes of %d", i, len(read[i].Output), read[i].OutputSize)
		}
	}
	if waiting, err := s.Runs().Waiting(ctx, "run"); err != nil || !waiting {
		t.Fatalf("waiting = %v %v", waiting, err)
	}

	finish := func(id string) {
		t.Helper()
		claimed, err := s.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{daltest.RunKind}, IDs: []string{id}, Now: time.Now(), Limit: 1}, Holder: "test", TTL: time.Minute})
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim %s: %+v %v", id, claimed, err)
		}
		claimed[0].Status = operations.StatusSucceeded
		if err := s.Operations().Finish(ctx, &claimed[0], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	releasable := func(ids ...string) []runstore.Wait {
		t.Helper()
		waits, err := s.Runs().ReleasableWaits(ctx, ids)
		if err != nil {
			t.Fatal(err)
		}
		return waits
	}
	if waits := releasable(); len(waits) != 0 {
		t.Fatalf("a wait for pending operations is releasable: %+v", waits)
	}
	finish("op-a")
	if waits := releasable("op-a"); len(waits) != 0 {
		t.Fatalf("a wait with a pending operation is releasable: %+v", waits)
	}
	finish("op-b")
	want := `[{RunID:run Seq:3 Operations:[op-a op-b]}]`
	if got := fmt.Sprintf("%+v", releasable("op-b")); got != want {
		t.Fatalf("releasable for op-b = %s, want %s", got, want)
	}
	if got := fmt.Sprintf("%+v", releasable()); got != want {
		t.Fatalf("releasable = %s, want %s", got, want)
	}
	if waits := releasable("other"); len(waits) != 0 {
		t.Fatalf("a wait is releasable for an operation it does not wait for: %+v", waits)
	}
	ended := json.RawMessage(`[{"id":"op-b","status":"succeeded"},{"id":"op-a","status":"succeeded"}]`)
	if done, err := s.Runs().FinishStep(ctx, "run", 3, ended, ""); err != nil || !done {
		t.Fatalf("finish the wait: %v %v", done, err)
	}
	if done, err := s.Runs().FinishStep(ctx, "run", 3, ended, ""); err != nil || done {
		t.Fatalf("a finished wait finished again: %v %v", done, err)
	}
	if waiting, err := s.Runs().Waiting(ctx, "run"); err != nil || waiting {
		t.Fatalf("waiting after release = %v %v", waiting, err)
	}
	if waits := releasable(); len(waits) != 0 {
		t.Fatalf("a released wait is releasable: %+v", waits)
	}

	if err := s.Runs().DropOutputs(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	read, err = s.Runs().Steps(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	// The repetitive page compresses below the kept size; the random one does not.
	if string(read[0].Output) != string(outputs[0]) || read[1].Output != nil || read[1].OutputSize != len(outputs[1]) ||
		string(read[2].Output) != string(outputs[2]) || string(read[3].Output) != string(ended) {
		t.Fatalf("kept outputs: %d %d %d %d bytes", len(read[0].Output), len(read[1].Output), len(read[2].Output), len(read[3].Output))
	}
}

func TestContinueRun_SQLite(t *testing.T)   { testContinueRun(t, daltest.SQLiteStore(t)) }
func TestContinueRun_Postgres(t *testing.T) { testContinueRun(t, daltest.PostgresStore(t)) }

// Continuing a run replaces its journal and carries state; a retry goes on
// from the generation that failed.
func testContinueRun(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	f := daltest.NewFixture(t, s)
	wf, err := s.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: f.Workspace, Key: "loop", Name: "Loop", Content: daltest.Script, CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := daltest.Run(ctx, s, runstore.CreateInput{ID: "loop", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-loop", EventType: "manual", Actor: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Runs().RecordSteps(ctx, []runstore.Step{
		{RunID: "loop", Seq: 0, Kind: runstore.StepRequest, Name: "GET /v1/items", InputHash: strings.Repeat("a", 64), Status: runstore.StepCompleted, Output: []byte(`{}`)},
		{RunID: "loop", Seq: 1, Kind: runstore.StepWait, Name: "operations", InputHash: strings.Repeat("b", 64), Status: runstore.StepCompleted, Operations: []string{"loop"}},
	}); err != nil {
		t.Fatal(err)
	}
	for generation, state := range []string{`{"cursor":"a"}`, `{"cursor":"b"}`} {
		if err := s.Runs().Continue(ctx, "loop", generation, json.RawMessage(state)); err != nil {
			t.Fatal(err)
		}
		run, err := s.Runs().Find(ctx, "loop")
		if err != nil || run.Generation != generation+1 || !jsonutil.Equal(run.State, []byte(state)) {
			t.Fatalf("after generation %d: %+v %v", generation, run, err)
		}
	}
	if steps, err := s.Runs().Steps(ctx, "loop"); err != nil || len(steps) != 0 {
		t.Fatalf("the journal outlived its generation: %+v %v", steps, err)
	}
	if err := s.Runs().Continue(ctx, "loop", 1, json.RawMessage(`{}`)); !errors.Is(err, runstore.ErrStaleGeneration) {
		t.Fatalf("a stale continuation: %v", err)
	}
	if err := daltest.Run(ctx, s, runstore.CreateInput{ID: "loop-retry", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: f.Workspace, EventID: "event-loop-retry", EventType: "manual", Actor: "alice", RetryOf: "loop"}); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Runs().Find(ctx, "loop-retry")
	if err != nil || retry.Generation != 2 || !jsonutil.Equal(retry.State, []byte(`{"cursor":"b"}`)) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
}
