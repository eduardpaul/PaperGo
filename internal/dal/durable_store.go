package dal

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"papergo/internal/dal/dberr"
)

// DurableStore owns SQL integration with DBOS's storage tables. Business logic
// selects terminal executions through DBOS; this store removes their payloads.
type DurableStore struct {
	db      *sql.DB
	dialect Dialect
	schema  string
}

func NewDurableStore(db *sql.DB, dialect Dialect, schema string) *DurableStore {
	return &DurableStore{db: db, dialect: dialect, schema: schema}
}
func (s *DurableStore) table(name string) string {
	if s.dialect == DialectPostgres {
		return pgx.Identifier{s.schema, name}.Sanitize()
	}
	return `"` + name + `"`
}

// RunCount is how many executions share a workflow name and status.
type RunCount struct {
	Name, Status string
	Count        int64
}

// RunCounts counts DBOS executions by workflow name and status, or
// PaperGo's runs by event and live status before DBOS has created its
// tables.
func (s *DurableStore) RunCounts(ctx context.Context) ([]RunCount, error) {
	presence := `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'workflow_status'`
	if s.dialect == DialectPostgres {
		presence = `SELECT count(*) FROM information_schema.tables WHERE table_schema = '` + strings.ReplaceAll(s.schema, "'", "''") + `' AND table_name = 'workflow_status'`
	}
	var present int
	if err := s.db.QueryRowContext(ctx, presence).Scan(&present); err != nil {
		return nil, dberr.Translate(err)
	}
	query := `SELECT event_type, live_status, count(*) FROM workflow_runs GROUP BY event_type, live_status ORDER BY event_type, live_status`
	if present > 0 {
		query = `SELECT name, status, count(*) FROM ` + s.table("workflow_status") + ` GROUP BY name, status ORDER BY name, status`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	out := []RunCount{}
	for rows.Next() {
		var row RunCount
		if err = rows.Scan(&row.Name, &row.Status, &row.Count); err != nil {
			return nil, dberr.Translate(err)
		}
		out = append(out, row)
	}
	return out, dberr.Translate(rows.Err())
}

// RunStatus is a DBOS execution's status and when it last changed.
type RunStatus struct {
	Status    string
	UpdatedAt time.Time
}

// Statuses reads the DBOS status of the given executions by ID, on tx when
// it is not nil. Executions DBOS does not know are left out. A SQLite writer
// holding the write lock reads on its own transaction: it must not wait for
// a second connection.
func (s *DurableStore) Statuses(ctx context.Context, tx *sql.Tx, ids []string) (map[string]RunStatus, error) {
	out := map[string]RunStatus{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	query := `SELECT workflow_uuid, status, updated_at FROM ` + s.table("workflow_status") + ` WHERE workflow_uuid IN (` + strings.Join(marks, ",") + `)`
	var rows *sql.Rows
	var err error
	if tx != nil {
		rows, err = tx.QueryContext(ctx, query, args...)
	} else {
		rows, err = s.db.QueryContext(ctx, query, args...)
	}
	if err != nil {
		return nil, dberr.Translate(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var updated int64
		if err = rows.Scan(&id, &status, &updated); err != nil {
			return nil, dberr.Translate(err)
		}
		out[id] = RunStatus{Status: status, UpdatedAt: time.UnixMilli(updated).UTC()}
	}
	return out, dberr.Translate(rows.Err())
}

func (s *DurableStore) DeleteMaintenanceRecords(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		marks[i] = fmt.Sprintf("$%d", i+1)
	}
	for _, table := range []struct{ name, column string }{{"notifications", "destination_uuid"}, {"workflow_events", "workflow_uuid"}, {"workflow_events_history", "workflow_uuid"}, {"streams", "workflow_uuid"}} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+s.table(table.name)+` WHERE `+table.column+` IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
			return dberr.Translate(err)
		}
	}
	return nil
}

// payloadTables hold a run's replay payloads, which account deletion removes.
var payloadTables = []string{"workflow_input", "workflow_output", "operation_outputs", "workflow_status"}

func (s *DurableStore) PurgeWorkspacePayloads(ctx context.Context, tx *sql.Tx, workspaceID string) error {
	for _, table := range payloadTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.table(table)+` WHERE workflow_uuid IN (SELECT id FROM workflow_runs WHERE workspace_id=$1)`, workspaceID); err != nil {
			return fmt.Errorf("purge workspace run %s: %w", table, dberr.Translate(err))
		}
	}
	return nil
}

// SealPurgedRuns removes the payloads DBOS wrote for the runs while the
// purge that removed them was in flight; call it after that purge commits.
// Its run.purge records make the guard refuse later writes. On PostgreSQL a
// write that passed the guard before the commit holds a shared lock on its
// run until it commits, so taking the lock here waits for it, and this
// transaction's statements then see its rows.
func (s *DurableStore) SealPurgedRuns(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return dberr.Translate(err)
	}
	defer tx.Rollback()
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		args := make([]any, len(chunk))
		marks := make([]string, len(chunk))
		for i, id := range chunk {
			args[i] = id
			marks[i] = fmt.Sprintf("$%d", i+1)
			if s.dialect == DialectPostgres {
				if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, id); err != nil {
					return dberr.Translate(err)
				}
			}
		}
		for _, table := range payloadTables {
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+s.table(table)+` WHERE workflow_uuid IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
				return fmt.Errorf("seal purged run %s: %w", table, dberr.Translate(err))
			}
		}
	}
	return dberr.Translate(tx.Commit())
}

// ProtectPurgedRuns prevents a late DBOS completion from recreating payloads
// after an account purge. DBOS owns the inserts, so the guard runs at storage.
// On PostgreSQL it holds a shared lock on the run, which SealPurgedRuns waits
// for.
func (s *DurableStore) ProtectPurgedRuns(ctx context.Context, app *Store) error {
	if s.dialect == DialectSQLite {
		for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs"} {
			statement := `CREATE TRIGGER IF NOT EXISTS papergo_` + table + `_owner BEFORE INSERT ON ` + s.table(table) + ` WHEN EXISTS(SELECT 1 FROM audit_events WHERE entity_type='workflow_run' AND entity_id=new.workflow_uuid AND action='run.purge') BEGIN SELECT RAISE(ABORT,'workflow run was removed');END`
			if _, err := app.execContext(ctx, statement); err != nil {
				return dberr.Translate(err)
			}
		}
		return nil
	}
	var schema string
	if err := app.queryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	function := pgx.Identifier{schema, "papergo_check_run_owner"}.Sanitize()
	audit := pgx.Identifier{schema, "audit_events"}.Sanitize()
	statement := `CREATE OR REPLACE FUNCTION ` + function + `() RETURNS trigger LANGUAGE plpgsql AS $papergo$ BEGIN PERFORM pg_advisory_xact_lock_shared(hashtextextended(NEW.workflow_uuid, 0)); IF EXISTS(SELECT 1 FROM ` + audit + ` WHERE entity_type='workflow_run' AND entity_id=NEW.workflow_uuid AND action='run.purge') THEN RAISE EXCEPTION 'workflow run was removed' USING ERRCODE='23514'; END IF; RETURN NEW; END $papergo$`
	if _, err := app.execContext(ctx, statement); err != nil {
		return dberr.Translate(err)
	}
	for _, table := range []string{"workflow_input", "workflow_output", "operation_outputs"} {
		if _, err := app.execContext(ctx, `CREATE OR REPLACE TRIGGER papergo_`+table+`_owner BEFORE INSERT ON `+s.table(table)+` FOR EACH ROW EXECUTE FUNCTION `+function+`() `); err != nil {
			return dberr.Translate(err)
		}
	}
	return nil
}
