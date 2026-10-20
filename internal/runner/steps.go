package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"papergo/internal/dal/dberr"
	"papergo/internal/dms"
)

// stepPointer is the only value DBOS stores for an exactly-once step. The
// step body stays in runner_step_results. A replay resolves the pointer
// against the workflow that is running, which is the forked run after a
// retry has copied the markers it reused.
type stepPointer struct {
	Kind string `json:"$papergo"`
	Step *int   `json:"step"`
}

func newStepPointer(stepID int) (json.RawMessage, error) {
	return json.Marshal(stepPointer{Kind: "step", Step: &stepID})
}

// stepTx runs fn as one exactly-once step. DBOS RunAsTransaction cannot
// share the application transaction (its callback gets no *sql.Tx), so the
// step's writes, the events they raise (and the runs those start) and a
// completion marker in runner_step_results commit together in one PaperGo
// write. A step retried or recovered after that commit finds the marker and
// returns the stored output instead of writing again. Markers are keyed by
// the DBOS step ID, which a replay assigns in the same order. DBOS records
// the pointer; the body is read back from the marker so a first run and a
// replay see the same value. The write takes the process's writer slot
// before a connection, like every other write: on SQLite, steps waiting for
// the write lock would otherwise hold every pooled connection and stall
// readers. It is a background write, so a burst of runs does not queue in
// front of requests or the dispatcher, and older runs' steps go first.
func stepTx(ctx dbos.Context, r *Runner, name string, fn func(context.Context, *dms.Service, func(dms.Event)) (json.RawMessage, error)) (json.RawMessage, error) {
	raw, err := dbos.RunAsStep(ctx, func(stepCtx context.Context) (json.RawMessage, error) {
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
		err = r.dms.Write(r.backgroundWrite(stepCtx, runID), func(t *dms.Service) error {
			purged, err := t.DAL.Runs().Purged(stepCtx, runID)
			if err != nil {
				return err
			}
			if purged {
				return dms.ErrNotFound
			}
			switch storedName, _, err := t.DAL.Runs().StepResult(stepCtx, runID, stepID); {
			case err == nil:
				if storedName != name {
					return fmt.Errorf("step %d of %s replayed as %q, recorded as %q", stepID, runID, name, storedName)
				}
				return nil
			case !dberr.IsNotFound(err):
				return err
			}
			out, err := fn(stepCtx, t, func(ev dms.Event) { t.Emit(stepCtx, ev) })
			if err != nil {
				return err
			}
			if len(out) == 0 {
				out = json.RawMessage("null")
			}
			return t.DAL.Runs().SaveStepResult(stepCtx, runID, stepID, name, []byte(out))
		})
		if err != nil {
			return nil, err
		}
		return newStepPointer(stepID)
	}, dbos.WithStepName(name), dbos.WithStepMaxRetries(3), dbos.WithStepBaseInterval(200*time.Millisecond), dbos.WithStepRetryPredicate(transient))
	if err != nil {
		return nil, err
	}
	return resolveStepPointer(ctx, r, raw)
}

func resolveStepPointer(ctx dbos.Context, r *Runner, raw json.RawMessage) (json.RawMessage, error) {
	var pointer stepPointer
	if err := json.Unmarshal(raw, &pointer); err != nil || pointer.Kind != "step" || pointer.Step == nil || *pointer.Step < 0 {
		return nil, errors.New("durable step reference is invalid")
	}
	runID, err := dbos.GetWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	var stored []byte
	if _, stored, err = r.dms.DAL.Runs().StepResult(ctx, runID, *pointer.Step); err != nil {
		return nil, fmt.Errorf("resolve step %d of %s: %w", *pointer.Step, runID, err)
	}
	return json.RawMessage(stored), nil
}

// transient errors are SQLite lock waits that outlived the busy timeout;
// everything else is the node's own failure and follows its retry policy.
func transient(err error) bool { return dberr.IsRetryable(err) }
