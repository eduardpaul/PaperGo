package runner

import (
	"context"
	"database/sql"
	"fmt"
)

// DBOS v1.6 keeps these payload tables separate from workflow_status, without
// cascading foreign keys. Purge them in the account's application transaction.
func (r *Runner) purgeHomeRunPayloads(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs", "workflow_status"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE workflow_uuid IN (SELECT id FROM workflow_runs WHERE workspace_id=?)", workspaceID); err != nil {
			return fmt.Errorf("purge Home run %s: %w", table, err)
		}
	}
	return nil
}

// A runtime step can finish after its account was deleted. Without these
// guards DBOS could insert a replay output again after its status was purged.
func (r *Runner) protectRunPayloads(ctx context.Context) error {
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs"} {
		statement := "CREATE TRIGGER IF NOT EXISTS papergo_" + table + "_owner BEFORE INSERT ON " + table + " WHEN EXISTS(SELECT 1 FROM audit_events WHERE entity_type='workflow_run' AND entity_id=new.workflow_uuid AND action='run.purge') BEGIN SELECT RAISE(ABORT,'workflow run was removed');END"
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
