package runner

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
	"time"
)

func waitTopic(kind, key string) string { return "wait:" + kind + ":" + key }

// Await delegates durable waiting and timeout checkpointing to DBOS.
func (x *durable) Await(node string, park workflow.Park) (json.RawMessage, error) {
	runID, err := dbos.GetWorkflowID(x.ctx)
	if err != nil {
		return nil, err
	}
	timeout := 100 * 365 * 24 * time.Hour
	if !park.Deadline.IsZero() {
		timeout = time.Until(park.Deadline)
		if timeout <= 0 {
			timeout = time.Nanosecond
		}
	}
	_, err = dbos.Recv[string](x.ctx, waitTopic(park.Kind, park.Key), timeout)
	if err != nil && !errors.Is(err, dbos.ErrTimeout) {
		return nil, err
	}
	timedOut := err != nil
	return stepTx(x.ctx, x.r, node+" result", func(ctx context.Context, t *dms.Service, _ func(dms.Event)) (json.RawMessage, error) {
		w, err := workflow.GetWait(ctx, t, park.Kind, park.Key)
		if err != nil {
			return nil, err
		}
		if w.RunID != runID {
			return nil, dms.ErrForbidden
		}
		if w.Status == "open" && timedOut {
			if err = workflow.FailWait(ctx, t, w.Kind, w.Key, "the wait reached its deadline"); err != nil {
				return nil, err
			}
			w, err = workflow.GetWait(ctx, t, w.Kind, w.Key)
			if err != nil {
				return nil, err
			}
		}
		if w.Status != "completed" {
			return nil, errors.New("the wait is not completed")
		}
		return w.Completion, nil
	})
}

// SignalWaits atomically publishes completed application waits into DBOS's
// durable inbox and records publication. Repeating a send is idempotent.
func (r *Runner) SignalWaits(ctx context.Context) error {
	return r.dms.Write(ctx, func(t *dms.Service) error {
		if err := workflow.ResolveWaits(ctx, t); err != nil {
			return err
		}
		if err := workflow.ExpireWaits(ctx, t, time.Now().UTC()); err != nil {
			return err
		}
		rows, err := workflow.CompletedParking(ctx, t)
		if err != nil {
			return err
		}
		for _, w := range rows {
			if err = dbos.Send(r.ctx, w.RunID, w.ID, waitTopic(w.Kind, w.Key), dbos.WithSendTransaction(t.SQLTx()), dbos.WithIdempotencyKey(w.ID)); err != nil {
				return err
			}
			if err = t.DAL.Waits().MarkNotified(ctx, w.ID, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
}
func parkedRuns(ctx context.Context, t *dms.Service, ids []string) (map[string]bool, error) {
	return workflow.ParkingRuns(ctx, t, ids)
}
func (r *Runner) cancelRun(ctx context.Context, id string) error {
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		if _, err := tasks.CancelRun(ctx, t, id); err != nil {
			return err
		}
		if err := workflow.CloseRunWaits(ctx, t, id); err != nil {
			return err
		}
		if err := t.DiscardRunStagedFiles(ctx, id); err != nil {
			return err
		}
		return t.DAL.Runs().Observe(ctx, id, runstore.StatusCancelled, time.Now().UTC(), false)
	}); err != nil {
		return err
	}
	return dbos.CancelWorkflow(r.ctx, id)
}
