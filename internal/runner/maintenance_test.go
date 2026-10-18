package runner

import (
	"database/sql"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

func TestMaintenanceRetentionKeepsActiveWorkAndApplicationRuns(t *testing.T) {
	savedPage := purgePage
	purgePage = 2
	t.Cleanup(func() { purgePage = savedPage })
	f := setup(t)
	user := f.workflow(t, "Folders", `{"triggers": [{"type": "folder.created", "collection_id": "$LIST"}], "flow": {"start": "end", "nodes": {"end": {"activity": "end"}}}}`)
	f.folder(t, f.list.ID, "Kept")
	f.runs(t, user.ID, 1)

	old := time.Date(2026, 10, 17, 22, 0, 0, 0, time.UTC)
	type scheduled struct {
		name string
		fn   func(dbos.Context, dbos.ScheduledWorkflowInput) (any, error)
		when time.Time
	}
	plan := []scheduled{
		{tickName, f.r.tick, old},
		{tickName, f.r.tick, old.Add(time.Minute)},
		{tickName, f.r.tick, old.Add(2 * time.Minute)},
		{tickName, f.r.tick, old.Add(3 * time.Minute)},
		{tickName, f.r.tick, old.Add(4 * time.Minute)},
		{tickName, f.r.tick, time.Now().UTC().Truncate(time.Second)},
		{searchEmbedName, f.r.sweepEmbeddings, old},
		{systemPurgeName, f.r.purgeSystem, old},
		{purgeName, f.r.purge, old},
	}
	ids := make([]string, len(plan))
	for i, run := range plan {
		ids[i] = "sched-" + run.name + "-" + run.when.Format(time.RFC3339Nano)
		handle, err := dbos.RunWorkflow(f.r.ctx, run.fn, dbos.ScheduledWorkflowInput{ScheduledTime: run.when}, dbos.WithWorkflowID(ids[i]))
		if err != nil {
			t.Fatal(run.name, err)
		}
		if _, err = handle.GetResult(); err != nil {
			t.Fatal(run.name, err)
		}
	}
	savedRetention := maintenanceRetention
	maintenanceRetention = time.Hour
	t.Cleanup(func() { maintenanceRetention = savedRetention })
	aged := time.Now().Add(-2 * time.Hour).UnixMilli()
	for _, id := range append(append([]string{}, ids[:5]...), ids[6:]...) {
		if _, err := f.db.SQL.Exec(`UPDATE workflow_status SET completed_at=? WHERE workflow_uuid=?`, aged, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.SQL.Exec(`UPDATE workflow_status SET status=?, completed_at=NULL WHERE workflow_uuid=?`, dbos.WorkflowStatusPending, ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL.Exec(`UPDATE workflow_status SET status=? WHERE workflow_uuid=?`, dbos.WorkflowStatusError, ids[4]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL.Exec(`INSERT INTO notifications (message_uuid, destination_uuid, topic, message) VALUES (?,?,?,?)`, "maint-note", ids[0], "diag", "tick"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SQL.Exec(`INSERT INTO workflow_events (workflow_uuid, key, value) VALUES (?,?,?)`, ids[0], "seen", "1"); err != nil {
		t.Fatal(err)
	}

	deleted, err := f.r.PurgeMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Three aged successful ticks span two pages, plus the embed sweep and
	// both retention workflows. The pending tick, the failed tick, the
	// recent tick and the application run stay.
	if deleted != 6 {
		t.Fatalf("deleted %d maintenance executions, want 6", deleted)
	}
	for _, id := range append([]string{ids[0], ids[1], ids[2]}, ids[6:]...) {
		if got := maintenanceCount(t, f.db.SQL, id); got != 0 {
			t.Fatalf("aged execution %s still stored: %d rows", id, got)
		}
	}
	for _, id := range []string{ids[3], ids[4], ids[5]} {
		if got := maintenanceCount(t, f.db.SQL, id); got == 0 {
			t.Fatalf("execution %s was collected", id)
		}
	}
	f.runs(t, user.ID, 1)
	var application int
	if err = f.db.SQL.QueryRow(`SELECT count(*) FROM workflow_status WHERE name=?`, runName).Scan(&application); err != nil || application == 0 {
		t.Fatal("application executions were collected", application, err)
	}

	if err = f.r.Shutdown(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	restarted := open(t, f.path, "node-b")
	if got := maintenanceCount(t, restarted.db.SQL, ids[0]); got != 0 {
		t.Fatalf("restart dispatched the collected tick again: %d rows", got)
	}
	if got := maintenanceCount(t, restarted.db.SQL, ids[5]); got == 0 {
		t.Fatal("restart lost the retained schedule execution")
	}
	if got := maintenanceCount(t, restarted.db.SQL, ids[3]); got == 0 {
		t.Fatal("restart lost the pending execution")
	}
	var runs int
	if err = restarted.db.SQL.QueryRow(`SELECT count(*) FROM workflow_runs WHERE workflow_id=?`, user.ID).Scan(&runs); err != nil || runs != 1 {
		t.Fatal("restart changed the application run", runs, err)
	}
}

func maintenanceCount(t *testing.T, db *sql.DB, id string) int {
	t.Helper()
	var status, inputs, outputs, steps, notes, events int
	for _, probe := range []struct {
		query string
		dest  *int
	}{
		{`SELECT count(*) FROM workflow_status WHERE workflow_uuid=?`, &status},
		{`SELECT count(*) FROM workflow_input WHERE workflow_uuid=?`, &inputs},
		{`SELECT count(*) FROM workflow_output WHERE workflow_uuid=?`, &outputs},
		{`SELECT count(*) FROM operation_outputs WHERE workflow_uuid=?`, &steps},
		{`SELECT count(*) FROM notifications WHERE destination_uuid=?`, &notes},
		{`SELECT count(*) FROM workflow_events WHERE workflow_uuid=?`, &events},
	} {
		if err := db.QueryRow(probe.query, id).Scan(probe.dest); err != nil {
			t.Fatal(err)
		}
	}
	return status + inputs + outputs + steps + notes + events
}
