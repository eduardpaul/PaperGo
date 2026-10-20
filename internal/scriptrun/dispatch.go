package scriptrun

import (
	"context"
	"slices"
	"time"

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

// Woken receives after Wake, for the dispatch maintenance task.
func (r *Runner) Woken() <-chan struct{} { return r.wake }

// Dispatch matches the undispatched events in the domain event log, oldest
// first, to workflows. For each batch, the runs it starts, the runs it
// cancels and the events' dispatched mark commit in one transaction, so a
// crash dispatches a batch again or not at all, and run IDs (one per
// workflow and event) make repeats harmless. It is a maintenance task.
func (r *Runner) Dispatch(ctx context.Context, _ time.Time) error {
	for ctx.Err() == nil {
		n, more, err := r.dispatchBatch(ctx)
		if err != nil || n < dispatchBatch && !more {
			return err
		}
	}
	return ctx.Err()
}

// dispatchBatch dispatches the oldest undispatched events. A
// permissions.changed event is delivered to each item and folder it affects
// (workflow.Listens, dms.PermissionTargets), PermissionBatch at a time: when
// more remain, its fanout cursor records the last one delivered, nothing
// after it is dispatched, and more is true, so events keep their order.
//
// Deleting an item cancels the active runs on it and the active selection
// runs it belongs to, except the run whose own step deleted it.
func (r *Runner) dispatchBatch(ctx context.Context) (n int, more bool, err error) {
	err = r.DMS.Write(ctx, func(t *dms.Service) error {
		more = false
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
		started, cancel, err := workflow.Match(ctx, t, events)
		if err != nil {
			return err
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
				if run.ID != e.CauseRunID && !slices.Contains(started, run.ID) {
					cancel = append(cancel, run.ID)
				}
			}
		}
		if err = t.CancelOperations(ctx, cancel); err != nil {
			return err
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
		more = false
	}
	return n, more, err
}

// Tick raises the schedule triggers due now. It is a maintenance task.
func (r *Runner) Tick(ctx context.Context, now time.Time) error {
	var skipped error
	err := r.DMS.Write(ctx, func(t *dms.Service) error {
		var err error
		_, skipped, err = workflow.Tick(ctx, t, now.UTC())
		return err
	})
	if skipped != nil {
		r.Log.Warn("workflow schedules could not run", "error", skipped)
	}
	return err
}
