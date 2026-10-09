package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

const dispatchBatch = 100

// Wake asks the dispatcher to look for new events now. Commits that logged
// events call it, so runs start moments after the change.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) dispatchLoop(ctx context.Context) {
	defer close(r.stopped)
	ticker := time.NewTicker(r.cfg.DispatchInterval)
	defer ticker.Stop()
	for {
		if _, err := r.Dispatch(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("dispatch workflow events", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-ticker.C:
		}
	}
}

// Dispatch matches the undispatched events in the domain event log, oldest
// first, to workflows. For each batch, the runs it starts (their workflow_runs
// rows and queued DBOS workflows) and the events' dispatched mark commit in
// one transaction, so a crash dispatches a batch again or not at all, and run
// IDs (one per workflow and event) make repeats harmless. The queued runs
// execute on whichever node's workers dequeue them.
func (r *Runner) Dispatch(ctx context.Context) (int, error) {
	total := 0
	for ctx.Err() == nil {
		n, err := r.dispatchBatch(ctx)
		total += n
		if err != nil || n < dispatchBatch {
			return total, err
		}
	}
	return total, ctx.Err()
}

func (r *Runner) dispatchBatch(ctx context.Context) (int, error) {
	var n int
	err := r.dms.Write(ctx, func(t *dms.Service) error {
		rows, err := t.Client.DomainEvent.Query().Where(domainevent.DispatchedAtIsNil()).Order(ent.Asc(domainevent.FieldCreatedAt), ent.Asc(domainevent.FieldID)).Limit(dispatchBatch).All(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}
		events := make([]dms.Event, len(rows))
		ids := make([]string, len(rows))
		for i, row := range rows {
			events[i] = dms.Event{ID: row.ID, Type: row.Type, WorkspaceID: row.WorkspaceID, Actor: row.Actor, Data: row.Data, Depth: row.Depth}
			if row.CollectionID != nil {
				events[i].CollectionID = *row.CollectionID
			}
			if row.ResourceID != nil {
				events[i].ResourceID = *row.ResourceID
			}
			ids[i] = row.ID
		}
		starts, err := workflow.Match(ctx, t, events)
		if err != nil {
			return err
		}
		for _, in := range starts {
			if _, err = dbos.Enqueue[workflow.Result](r.ctx, runQueue, runName, in, dbos.WithEnqueueWorkflowID(in.RunID), dbos.WithEnqueueTransaction(t.SQLTx())); err != nil {
				return fmt.Errorf("start run %s: %w", in.RunID, err)
			}
		}
		if err = t.Client.DomainEvent.Update().Where(domainevent.IDIn(ids...)).SetDispatchedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return err
		}
		n = len(rows)
		return nil
	})
	return n, err
}
