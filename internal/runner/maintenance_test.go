package runner

import (
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
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
	s := f.db.DAL
	status := daltest.DBOS(s, "workflow_status")
	for _, id := range append(append([]string{}, ids[:5]...), ids[6:]...) {
		daltest.Update(t, s, status, daltest.Row{"completed_at": aged}, daltest.Row{"workflow_uuid": id})
	}
	daltest.Update(t, s, status, daltest.Row{"status": string(dbos.WorkflowStatusPending), "completed_at": nil}, daltest.Row{"workflow_uuid": ids[3]})
	daltest.Update(t, s, status, daltest.Row{"status": string(dbos.WorkflowStatusError)}, daltest.Row{"workflow_uuid": ids[4]})
	daltest.Insert(t, s, daltest.DBOS(s, "notifications"), daltest.Row{"message_uuid": "maint-note", "destination_uuid": ids[0], "topic": "diag", "message": "tick"})
	daltest.Insert(t, s, daltest.DBOS(s, "workflow_events"), daltest.Row{"workflow_uuid": ids[0], "key": "seen", "value": "1"})

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
		if got := maintenanceCount(t, s, id); got != 0 {
			t.Fatalf("aged execution %s still stored: %d rows", id, got)
		}
	}
	for _, id := range []string{ids[3], ids[4], ids[5]} {
		if got := maintenanceCount(t, s, id); got == 0 {
			t.Fatalf("execution %s was collected", id)
		}
	}
	f.runs(t, user.ID, 1)
	if application := daltest.Count(t, s, status, daltest.Row{"name": runName}); application == 0 {
		t.Fatal("application executions were collected")
	}

	if err = f.r.Shutdown(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	restarted := open(t, f.path, "node-b")
	if got := maintenanceCount(t, restarted.db.DAL, ids[0]); got != 0 {
		t.Fatalf("restart dispatched the collected tick again: %d rows", got)
	}
	if got := maintenanceCount(t, restarted.db.DAL, ids[5]); got == 0 {
		t.Fatal("restart lost the retained schedule execution")
	}
	if got := maintenanceCount(t, restarted.db.DAL, ids[3]); got == 0 {
		t.Fatal("restart lost the pending execution")
	}
	if runs := daltest.Count(t, restarted.db.DAL, "workflow_runs", daltest.Row{"workflow_id": user.ID}); runs != 1 {
		t.Fatal("restart changed the application run", runs)
	}
}

// maintenanceCount counts the rows the engine keeps of an execution.
func maintenanceCount(t *testing.T, s *dal.Store, id string) int {
	t.Helper()
	n := daltest.Count(t, s, daltest.DBOS(s, "notifications"), daltest.Row{"destination_uuid": id})
	for _, table := range []string{"workflow_status", "workflow_input", "workflow_output", "operation_outputs", "workflow_events"} {
		n += daltest.Count(t, s, daltest.DBOS(s, table), daltest.Row{"workflow_uuid": id})
	}
	return n
}
