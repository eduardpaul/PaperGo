package runner

import (
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountDeletionPurgesRuntimeReplayPayloads(t *testing.T) {
	f := open(t, filepath.Join(t.TempDir(), "home.db"), "home-node")
	home, err := f.dms.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	f.ws, err = f.dms.Get(ctx, "alice", home.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	f.list, err = f.dms.Get(ctx, "alice", home.InboxLibraryID)
	if err != nil {
		t.Fatal(err)
	}
	wf := f.workflow(t, "Personal flow", `{"triggers":[{"type":"item.created","collection_id":"$LIST"}],"flow":{"start":"emit","nodes":{"emit":{"activity":"event.raise","inputs":{"event":"private","data":{"text":"Personal output"}}}}}}`)
	if _, err = f.dms.Create(ctx, "alice", home.InboxLibraryID, dms.CreateResource{Kind: "item", Name: "Private", Values: map[string]any{"description": "Private contents"}}); err != nil {
		t.Fatal(err)
	}
	runs := f.runs(t, wf.ID, 1)
	if runs[0].Status != "completed" {
		t.Fatal(runs)
	}
	if before := daltest.Count(t, f.db.DAL, daltest.DBOS(f.db.DAL, "operation_outputs"), daltest.Row{"workflow_uuid": runs[0].ID}); before == 0 {
		t.Fatal("no replay output to purge")
	}
	if err = f.dms.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	f.noPayloads(t, runs[0].ID)
	for table, row := range map[string]daltest.Row{
		"workflow_input":    {"workflow_uuid": runs[0].ID, "inputs": "private"},
		"workflow_output":   {"workflow_uuid": runs[0].ID, "output": "private"},
		"operation_outputs": {"workflow_uuid": runs[0].ID, "function_id": 999, "function_name": "late", "output": "private"},
	} {
		if err = daltest.TryInsert(f.db.DAL, daltest.DBOS(f.db.DAL, table), row); err == nil {
			t.Fatal("purged run recreated payload", table)
		}
	}
}

func TestDeletingHomeStopsInFlightRunPayloadWrites(t *testing.T) {
	f := open(t, filepath.Join(t.TempDir(), "active-home.db"), "active-home-node")
	home, err := f.dms.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	f.ws, err = f.dms.Get(ctx, "alice", home.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	f.list, err = f.dms.Get(ctx, "alice", home.InboxLibraryID)
	if err != nil {
		t.Fatal(err)
	}
	wf := f.workflow(t, "Waiting personal flow", `{"triggers":[{"type":"item.created","collection_id":"$LIST"}],"flow":{"start":"wait","nodes":{"wait":{"activity":"delay","inputs":{"duration":"1s"},"next":{"done":"emit"}},"emit":{"activity":"event.raise","inputs":{"event":"late-private","data":{"text":"Personal output"}}}}}}`)
	if _, err = f.dms.Create(ctx, "alice", home.InboxLibraryID, dms.CreateResource{Kind: "item", Name: "Private"}); err != nil {
		t.Fatal(err)
	}
	runID := f.waitingRun(t, wf.ID)
	if err = f.dms.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	// Let the in-flight delay finish: subsequent steps and SDK output writes
	// must still be unable to recreate private replay payloads.
	time.Sleep(1500 * time.Millisecond)
	f.noPayloads(t, runID)
	if n := daltest.Count(t, f.db.DAL, "runner_step_results", daltest.Row{"run_id": runID}); n != 0 {
		t.Fatal("late step marker survived", n)
	}
}

// noPayloads checks that the engine keeps no inputs, outputs or status of a
// run.
func (f *fixture) noPayloads(t *testing.T, runID string) {
	t.Helper()
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs", "workflow_status"} {
		if n := daltest.Count(t, f.db.DAL, daltest.DBOS(f.db.DAL, table), daltest.Row{"workflow_uuid": runID}); n != 0 {
			t.Fatal("runtime payload survived", table, n)
		}
	}
}

// waitingRun waits for a run of the workflow to reach its step named wait.
func (f *fixture) waitingRun(t *testing.T, workflowID string) string {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		for _, id := range daltest.Values(t, f.db.DAL, "workflow_runs", "id", daltest.Row{"workflow_id": workflowID}) {
			if daltest.Count(t, f.db.DAL, "runner_step_results", daltest.Row{"run_id": id, "step": "wait"}) > 0 {
				return id.(string)
			}
		}
	}
	t.Fatal("run never reached its wait")
	return ""
}
