package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	runstore "papergo/internal/dal/runs"
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
	var synced time.Time
	active := 0
	for {
		if _, err := r.Dispatch(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("dispatch workflow events", "error", err)
		}
		// Statuses follow each commit, which lease takeover relies on. While
		// a backlog drains, commits wake the loop many times a second and the
		// sync reads every active run, so it then keeps the interval.
		if active <= liveBatch || time.Since(synced) >= r.cfg.DispatchInterval {
			synced = time.Now()
			var err error
			if active, err = r.SyncLiveRuns(ctx); err != nil && ctx.Err() == nil {
				r.log.Error("observe workflow statuses", "error", err)
			}
		}
		if err := r.SignalWaits(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("signal completed waits", "error", err)
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
// runs as waiting. It reads in the dispatch transaction: on SQLite that
// transaction holds the write lock, and the connections a read elsewhere
// needs can all be held by writers waiting for that lock.
func (r *Runner) activeRuns(ctx context.Context, t *dms.Service, ids []string) (map[string]runstore.Status, error) {
	statuses, err := r.durable.Statuses(ctx, t.SQLTx(), ids)
	if err != nil {
		return nil, err
	}
	parked, err := parkedRuns(ctx, t, ids)
	if err != nil {
		return nil, err
	}
	out := map[string]runstore.Status{}
	for id, s := range statuses {
		switch state := status(dbos.WorkflowStatusType(s.Status)); {
		case (state == runstore.StatusRunning || state == runstore.StatusQueued) && parked[id]:
			out[id] = runstore.StatusWaiting
		case state == runstore.StatusQueued || state == runstore.StatusRunning:
			out[id] = state
		}
	}
	return out, nil
}

func (r *Runner) cancelActive(id string) error {
	runs := []Run{{ID: id}}
	if err := r.attach(runs, false); err != nil {
		return err
	}
	if runs[0].Status.Finished() {
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
		rows, err := t.DAL.Events().Undispatched(ctx, dispatchBatch)
		if err != nil || len(rows) == 0 {
			return err
		}
		events := make([]dms.Event, 0, len(rows))
		ids := make([]string, 0, len(rows))
		// This cache belongs to the transaction, so a failed dispatch retries
		// default creation along with the writes it rolled back.
		ensured := map[string]bool{}
		for _, row := range rows {
			e := dms.Event{ID: row.ID, Type: row.Type, WorkspaceID: row.WorkspaceID, CollectionID: row.CollectionID, ResourceID: row.ResourceID, Actor: row.Actor, CauseRunID: row.CauseRunID, Data: row.Data, Depth: row.Depth}
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
				targets, err := t.PermissionTargets(ctx, e, row.FanoutCursor, workflow.PermissionBatch)
				if err != nil {
					return err
				}
				events = append(events, workflow.PermissionDeliveries(e, targets)...)
				if len(targets) == workflow.PermissionBatch {
					if err = t.DAL.Events().AdvanceFanout(ctx, row.ID, targets[len(targets)-1].ID); err != nil {
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
			memberships, err := t.DAL.Runs().Items(ctx, nil, e.ResourceID)
			if err != nil {
				return err
			}
			for _, m := range memberships {
				if m.RunID != e.CauseRunID {
					cancel = append(cancel, m.RunID)
				}
			}
			// Runs on the item, except those its deletion started.
			runs, err := t.DAL.Runs().List(ctx, runstore.Filter{ItemIDs: []string{e.ResourceID}, Statuses: runstore.Active, ExcludeEventTypes: []string{dms.EventItemDeleted, dms.EventItemPurged}})
			if err != nil {
				return err
			}
			for _, run := range runs {
				id := run.ID
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
			if err = t.DAL.Events().Dispatch(ctx, ids, time.Now().UTC()); err != nil {
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
