package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/ent/workflowrunitem"
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
		if err := r.SyncLiveRuns(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("observe workflow statuses", "error", err)
		}
		if err := r.ResumeDecided(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("resume runs whose task completed", "error", err)
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
//
// Deleting an item cancels the active selection runs it belongs to, except
// the run whose own step deleted it.
func (r *Runner) Dispatch(ctx context.Context) (int, error) {
	total := 0
	for ctx.Err() == nil {
		n, cancel, err := r.dispatchBatch(ctx)
		total += n
		// DBOS cancels on its own connection, so after the batch committed.
		for _, id := range cancel {
			if e := r.cancelActive(id); e != nil && err == nil {
				err = e
			}
		}
		if err != nil || n < dispatchBatch {
			return total, err
		}
	}
	return total, ctx.Err()
}

// activeRuns reports which runs DBOS still has queued or running. It reads
// on DBOS's own connection, which sees committed state only.
func (r *Runner) activeRuns(ids []string) (map[string]bool, error) {
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
	if err != nil {
		return nil, err
	}
	parked, err := parkedRuns(context.Background(), r.dms, ids)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, s := range statuses {
		if state := status(s.Status); state == "queued" || state == "running" || parked[s.ID] {
			out[s.ID] = true
		}
	}
	return out, nil
}

func (r *Runner) cancelActive(id string) error {
	runs := []Run{{ID: id}}
	if err := r.attach(runs, false); err != nil {
		return err
	}
	if runs[0].Status != "queued" && runs[0].Status != "running" && runs[0].Status != "waiting" {
		return nil
	}
	r.log.Info("cancel run: a member was deleted, or a newer run replaced it", "run_id", id)
	return r.cancelRun(context.Background(), id)
}

func (r *Runner) dispatchBatch(ctx context.Context) (int, []string, error) {
	var n int
	var cancel []string
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
			if row.CauseRunID != nil {
				events[i].CauseRunID = *row.CauseRunID
			}
			ids[i] = row.ID
		}
		starts, replaced, err := workflow.Match(ctx, t, events, r.activeRuns)
		if err != nil {
			return err
		}
		cancel = append(cancel, replaced...)
		for _, e := range events {
			if (e.Type != dms.EventItemDeleted && e.Type != dms.EventItemPurged) || e.ResourceID == "" {
				continue
			}
			memberships, err := t.Client.WorkflowRunItem.Query().Where(workflowrunitem.ItemIDEQ(e.ResourceID)).All(ctx)
			if err != nil {
				return err
			}
			for _, m := range memberships {
				if m.RunID != e.CauseRunID {
					cancel = append(cancel, m.RunID)
				}
			}
		}
		for _, in := range starts {
			if _, err = dbos.Enqueue[workflow.Result](r.ctx, runQueue, runName, in, dbos.WithEnqueueWorkflowID(in.RunID), dbos.WithEnqueueTransaction(t.SQLTx())); err != nil {
				return fmt.Errorf("start run %s: %w", in.RunID, err)
			}
		}
		if err = t.Client.DomainEvent.Update().Where(domainevent.IDIn(ids...)).SetDispatchedAt(time.Now().UTC()).Exec(ctx); err != nil {
			return err
		}
		items := make([]string, 0, len(events))
		for _, e := range events {
			items = append(items, e.ResourceID)
		}
		if err = t.CollectItemRevisions(ctx, items); err != nil {
			return err
		}
		n = len(rows)
		return nil
	})
	if err != nil {
		cancel = nil
	}
	return n, cancel, err
}
