package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/internal/dms"
)

// stepTx runs fn as one exactly-once step. DBOS RunAsTransaction cannot
// carry Ent (its callback gets no *sql.Tx), so the step's writes, the events
// they raise (and the runs those start) and a completion marker in
// runner_step_results commit together in one PaperGo transaction. A step
// retried or recovered after that commit finds the marker and returns the
// stored output instead of writing again. Markers are keyed by the DBOS step
// ID, which a replay assigns in the same order, and the output always comes
// back decoded from the marker so a first run and a replay see the same value.
func stepTx(ctx dbos.Context, r *Runner, name string, fn func(context.Context, *dms.Service, func(dms.Event)) (json.RawMessage, error)) (json.RawMessage, error) {
	return dbos.RunAsStep(ctx, func(stepCtx context.Context) (json.RawMessage, error) {
		dctx, ok := stepCtx.(dbos.Context)
		if !ok {
			return nil, errors.New("step context is not a DBOS context")
		}
		runID, err := dbos.GetWorkflowID(dctx)
		if err != nil {
			return nil, err
		}
		stepID, err := dbos.GetStepID(dctx)
		if err != nil {
			return nil, err
		}
		tx, err := r.db.BeginTx(stepCtx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		var (
			stored     []byte
			storedName string
		)
		switch err = tx.QueryRowContext(stepCtx, `SELECT step, output FROM runner_step_results WHERE run_id=? AND step_id=?`, runID, stepID).Scan(&storedName, &stored); {
		case err == nil:
			if storedName != name {
				return nil, fmt.Errorf("step %d of %s replayed as %q, recorded as %q", stepID, runID, name, storedName)
			}
			return json.RawMessage(stored), nil
		case !errors.Is(err, sql.ErrNoRows):
			return nil, err
		}
		var out json.RawMessage
		if err = r.dms.WriteTx(stepCtx, tx, func(t *dms.Service) error {
			var e error
			out, e = fn(stepCtx, t, func(ev dms.Event) { t.Emit(stepCtx, ev) })
			return e
		}); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			out = json.RawMessage("null")
		}
		if _, err = tx.ExecContext(stepCtx, `INSERT INTO runner_step_results (run_id, step_id, step, output) VALUES (?,?,?,?)`, runID, stepID, name, []byte(out)); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return out, nil
	}, dbos.WithStepName(name), dbos.WithStepMaxRetries(3), dbos.WithStepBaseInterval(200*time.Millisecond), dbos.WithStepRetryPredicate(transient))
}

// transient errors are SQLite lock waits that outlived the busy timeout;
// everything else is the node's own failure and follows its retry policy.
func transient(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}
