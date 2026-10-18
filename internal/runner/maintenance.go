package runner

import (
	"context"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
)

// maintenanceRetention keeps successful maintenance executions long enough
// for scheduler deduplication and recent diagnostics. Schedule identity is
// the workflow id sched-<name>-<time>, automatic backfill is off, and the
// longest of these schedules repeats hourly, so two hours covers a restart
// without dispatching the same tick again. Tests lower it. Failures stay
// for the full run retention.
var maintenanceRetention = 2 * time.Hour

// maintenanceNames are the durable schedules that are not application runs.
// Naming them keeps this cleanup off workflow_runs, step markers, waits and
// staged files, which Purge and PurgeSystemRuns collect together.
func maintenanceNames() []string {
	return []string{tickName, searchEmbedName, systemPurgeName, purgeName}
}

func maintenanceTerminal(status dbos.WorkflowStatusType) bool {
	switch status {
	case dbos.WorkflowStatusSuccess, dbos.WorkflowStatusCancelled, dbos.WorkflowStatusError, dbos.WorkflowStatusMaxRecoveryAttemptsExceeded:
		return true
	default:
		return false
	}
}

// PurgeMaintenance deletes terminal executions of the maintenance schedules
// whose age is past retention, including their DBOS inputs, outputs and
// runtime records. Pending and running executions stay. A crash between
// pages or between child rows and the status row continues on the next pass.
func (r *Runner) PurgeMaintenance(ctx context.Context) (int, error) {
	now := time.Now()
	successCutoff := now.Add(-min(maintenanceRetention, r.cfg.Retention))
	failureCutoff := now.Add(-r.cfg.Retention)
	deleted := 0
	for _, name := range maintenanceNames() {
		n, err := r.purgeMaintenance(ctx, name, []dbos.WorkflowStatusType{dbos.WorkflowStatusSuccess, dbos.WorkflowStatusCancelled}, successCutoff)
		if err != nil {
			return deleted, err
		}
		deleted += n
		n, err = r.purgeMaintenance(ctx, name, []dbos.WorkflowStatusType{dbos.WorkflowStatusError, dbos.WorkflowStatusMaxRecoveryAttemptsExceeded}, failureCutoff)
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}

func (r *Runner) purgeMaintenance(ctx context.Context, name string, status []dbos.WorkflowStatusType, cutoff time.Time) (int, error) {
	deleted := 0
	for {
		done, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterName(name), dbos.WithFilterStatus(status...),
			dbos.WithFilterCompletedBefore(cutoff), dbos.WithFilterLimit(purgePage), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
		if err != nil || len(done) == 0 {
			return deleted, err
		}
		ids := make([]string, 0, len(done))
		for _, row := range done {
			if maintenanceTerminal(row.Status) {
				ids = append(ids, row.ID)
			}
		}
		if len(ids) == 0 {
			return deleted, nil
		}
		if err = r.deleteMaintenance(ctx, ids); err != nil {
			return deleted, err
		}
		deleted += len(ids)
		if len(done) < purgePage {
			return deleted, nil
		}
	}
}

// deleteMaintenance removes DBOS records for executions already selected as
// terminal maintenance work. Child rows go first: DeleteWorkflows removes
// the status row, and a crash before that leaves the execution visible to
// the next pass. Application runs never reach this function.
func (r *Runner) deleteMaintenance(ctx context.Context, ids []string) error {
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i], marks[i] = id, "?"
	}
	in := strings.Join(marks, ",")
	for _, table := range []struct{ name, column string }{
		{"notifications", "destination_uuid"},
		{"workflow_events", "workflow_uuid"},
		{"workflow_events_history", "workflow_uuid"},
		{"streams", "workflow_uuid"},
	} {
		if _, err := r.system.ExecContext(ctx, `DELETE FROM `+table.name+` WHERE `+table.column+` IN (`+in+`)`, args...); err != nil {
			return err
		}
	}
	return dbos.DeleteWorkflows(r.ctx, ids)
}

func (r *Runner) collectMaintenance(ctx context.Context) error {
	n, err := r.PurgeMaintenance(ctx)
	if n > 0 {
		r.log.Info("collected maintenance executions", "deleted", n)
	}
	return err
}
