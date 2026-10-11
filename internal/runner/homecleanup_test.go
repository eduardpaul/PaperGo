package runner

import (
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
	var before int
	if err = f.db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM operation_outputs WHERE workflow_uuid=?", runs[0].ID).Scan(&before); err != nil || before == 0 {
		t.Fatal("no replay output to purge", before, err)
	}
	if err = f.dms.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs", "workflow_status"} {
		var n int
		if err = f.db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE workflow_uuid=?", runs[0].ID).Scan(&n); err != nil || n != 0 {
			t.Fatal("runtime payload survived", table, n, err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO workflow_input(workflow_uuid,inputs) VALUES(?,'private')",
		"INSERT INTO workflow_output(workflow_uuid,output) VALUES(?,'private')",
		"INSERT INTO operation_outputs(workflow_uuid,function_id,function_name,output) VALUES(?,999,'late','private')",
	} {
		if _, err = f.db.SQL.ExecContext(ctx, statement, runs[0].ID); err == nil {
			t.Fatal("purged run recreated payload", statement)
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
	var runID string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		err = f.db.SQL.QueryRowContext(ctx, "SELECT r.id FROM workflow_runs r JOIN runner_step_results s ON s.run_id=r.id WHERE r.workflow_id=? AND s.step='wait'", wf.ID).Scan(&runID)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("run never reached its delay", err)
	}
	if err = f.dms.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	// Let the in-flight delay finish: subsequent steps and SDK output writes
	// must still be unable to recreate private replay payloads.
	time.Sleep(1500 * time.Millisecond)
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs", "workflow_status"} {
		var n int
		if err = f.db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE workflow_uuid=?", runID).Scan(&n); err != nil || n != 0 {
			t.Fatal("late runtime payload survived", table, n, err)
		}
	}
	var n int
	if err = f.db.SQL.QueryRowContext(ctx, "SELECT count(*) FROM runner_step_results WHERE run_id=?", runID).Scan(&n); err != nil || n != 0 {
		t.Fatal("late step marker survived", n, err)
	}
}
