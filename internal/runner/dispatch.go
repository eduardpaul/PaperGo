package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/ent/workflowrun"
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
// Deleting an item cancels the active runs on it and the active selection
// runs it belongs to, except the run whose own step deleted it.
func (r *Runner) Dispatch(ctx context.Context) (int, error) {
	total := 0
	for ctx.Err() == nil {
		n, more, cancel, err := r.dispatchBatch(ctx)
		total += n
		// DBOS cancels on its own connection, so after the batch committed.
		for _, id := range cancel {
			if e := r.cancelActive(id); e != nil && err == nil {
				err = e
			}
		}
		if err != nil || n < dispatchBatch && !more {
			return total, err
		}
	}
	return total, ctx.Err()
}

// activeRuns reports the runs DBOS still has queued or running, and parked
// runs as waiting. It reads on DBOS's own connection, which sees committed
// state only.
func (r *Runner) activeRuns(ids []string) (map[string]string, error) {
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
	if err != nil {
		return nil, err
	}
	parked, err := parkedRuns(context.Background(), r.dms, ids)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range statuses {
		switch state := status(s.Status); {
		case parked[s.ID]:
			out[s.ID] = "waiting"
		case state == "queued" || state == "running":
			out[s.ID] = state
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
	r.log.Info("cancel run: its item was deleted, or a newer run replaced it", "run_id", id)
	return r.cancelRun(context.Background(), id)
}

// dispatchBatch dispatches the oldest undispatched events. A permissions.changed
// event is delivered to each item and folder it affects (workflow.Listens,
// dms.PermissionTargets), PermissionBatch at a time: when more remain, its
// fanout cursor records the last one delivered, nothing after it is
// dispatched, and more is true, so events keep their order.
func (r *Runner) dispatchBatch(ctx context.Context) (n int, more bool, cancel []string, err error) {
	err = r.dms.Write(ctx, func(t *dms.Service) error {
		more, cancel = false, nil
		rows, err := t.Client.DomainEvent.Query().Where(domainevent.DispatchedAtIsNil()).Order(ent.Asc(domainevent.FieldCreatedAt), ent.Asc(domainevent.FieldID)).Limit(dispatchBatch).All(ctx)
		if err != nil || len(rows) == 0 {
			return err
		}
		events := make([]dms.Event, 0, len(rows))
		ids := make([]string, 0, len(rows))
		// This cache belongs to the transaction, so a failed dispatch retries
		// default creation along with the writes it rolled back.
		ensured := map[string]bool{}
		for _, row := range rows {
			e := dms.Event{ID: row.ID, Type: row.Type, WorkspaceID: row.WorkspaceID, Actor: row.Actor, Data: row.Data, Depth: row.Depth}
			if row.CollectionID != nil {
				e.CollectionID = *row.CollectionID
			}
			if row.ResourceID != nil {
				e.ResourceID = *row.ResourceID
			}
			if row.CauseRunID != nil {
				e.CauseRunID = *row.CauseRunID
			}
			// Default built-ins exist before the workspace's first event matches.
			if !ensured[e.WorkspaceID] {
				if err := workflow.EnsureDefaults(ctx, t, e.WorkspaceID); err != nil {
					return err
				}
				ensured[e.WorkspaceID] = true
			}
			if e.Type != dms.EventPermissionsChanged {
				events, ids = append(events, e), append(ids, row.ID)
				continue
			}
			listening, err := workflow.Listens(ctx, t, e)
			if err != nil {
				return err
			}
			if listening {
				after := ""
				if row.FanoutCursor != nil {
					after = *row.FanoutCursor
				}
				targets, err := t.PermissionTargets(ctx, e, after, workflow.PermissionBatch)
				if err != nil {
					return err
				}
				events = append(events, workflow.PermissionDeliveries(e, targets)...)
				if len(targets) == workflow.PermissionBatch {
					if err = t.Client.DomainEvent.UpdateOneID(row.ID).SetFanoutCursor(targets[len(targets)-1].ID).Exec(ctx); err != nil {
						return err
					}
					more = true
					break
				}
			}
			ids = append(ids, row.ID)
		}
		starts, replaced, err := workflow.Match(ctx, t, events, r.activeRuns)
		if err != nil {
			return err
		}
		cancel = append(cancel, replaced...)
		started := map[string]bool{}
		for _, in := range starts {
			started[in.RunID] = true
		}
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
			// Runs on the item, except those its deletion started.
			runs, err := t.Client.WorkflowRun.Query().Where(workflowrun.ItemIDEQ(e.ResourceID), workflowrun.LiveStatusIn("queued", "running", "waiting"),
				workflowrun.EventTypeNotIn(dms.EventItemDeleted, dms.EventItemPurged)).IDs(ctx)
			if err != nil {
				return err
			}
			for _, id := range runs {
				if id != e.CauseRunID && !started[id] {
					cancel = append(cancel, id)
				}
			}
		}
		for _, in := range starts {
			queue, opts := runQueue, []dbos.EnqueueOption{dbos.WithEnqueueWorkflowID(in.RunID), dbos.WithEnqueueTransaction(t.SQLTx())}
			switch {
			case in.System:
				// Coalesce runs of one item share a partition; others get their own.
				partition := in.Partition
				if partition == "" {
					partition = in.RunID
				}
				queue, opts = systemQueue, append(opts, dbos.WithEnqueueQueuePartitionKey(partition))
			case in.Partition != "":
				// Coalesce: runs of one workflow on one item execute one at a time.
				queue, opts = serialQueue, append(opts, dbos.WithEnqueueQueuePartitionKey(in.Partition))
			}
			if _, err = dbos.Enqueue[workflow.Result](r.ctx, queue, runName, in, opts...); err != nil {
				return fmt.Errorf("start run %s: %w", in.RunID, err)
			}
		}
		if len(ids) > 0 {
			if err = t.Client.DomainEvent.Update().Where(domainevent.IDIn(ids...)).SetDispatchedAt(time.Now().UTC()).Exec(ctx); err != nil {
				return err
			}
		}
		items := make([]string, 0, len(events))
		for _, e := range events {
			if e.Type != dms.EventPermissionsChanged {
				items = append(items, e.ResourceID)
			}
		}
		if err = t.CollectItemRevisions(ctx, items); err != nil {
			return err
		}
		n = len(ids)
		return nil
	})
	if err != nil {
		cancel, more = nil, false
	}
	return n, more, cancel, err
}
