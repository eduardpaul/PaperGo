package scriptrun

import (
	"context"
	"time"

	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// purgePage is how many finished runs a retention pass deletes at a time.
var purgePage = 200

// retention is how long finished runs and dispatched events are kept.
func (r *Runner) retention() time.Duration {
	if r.Retention > 0 {
		return r.Retention
	}
	return 30 * 24 * time.Hour
}

// systemRetention is how long successful automatic runs of system
// built-ins are kept; retention is a ceiling.
func (r *Runner) systemRetention() time.Duration {
	kept := 24 * time.Hour
	if r.SystemRetention > 0 {
		kept = r.SystemRetention
	}
	return min(kept, r.retention())
}

// Collect applies run retention at now: it deletes the runs that finished
// before the retention with their steps, logs and staged files, the
// successful automatic runs of system built-ins before theirs, and the
// events dispatched before the retention, and collects old revisions. Each
// page is its own transaction, so a large backlog releases the writer
// between pages. It is a maintenance task.
func (r *Runner) Collect(ctx context.Context, now time.Time) error {
	cutoff := now.UTC().Add(-r.retention())
	for {
		var n int
		err := r.DMS.Write(ctx, func(t *dms.Service) error {
			done, err := t.DAL.Operations().List(ctx, operations.Filter{Kinds: []string{dms.OperationKindRun}, Finished: true, UpdatedThrough: &cutoff, Limit: purgePage})
			if err != nil {
				return err
			}
			n = len(done)
			ids := make([]string, len(done))
			for i, op := range done {
				ids[i] = op.ID
			}
			return deleteRuns(ctx, t, ids)
		})
		if err != nil {
			return err
		}
		if n < purgePage {
			break
		}
	}
	if err := r.collectSystemRuns(ctx, now); err != nil {
		return err
	}
	for {
		var n int64
		err := r.DMS.Write(ctx, func(t *dms.Service) error {
			var err error
			n, err = t.DAL.Events().DeleteDispatched(ctx, cutoff, purgePage)
			return err
		})
		if err != nil {
			return err
		}
		if n < int64(purgePage) {
			break
		}
	}
	return r.DMS.Write(ctx, func(t *dms.Service) error { return t.CollectRevisions(ctx) })
}

// collectSystemRuns deletes the successful automatic runs of system
// built-ins that completed before systemRetention. Failures and manual runs
// stay for the full retention.
func (r *Runner) collectSystemRuns(ctx context.Context, now time.Time) error {
	keys := workflow.SystemBuiltIns()
	if len(keys) == 0 {
		return nil
	}
	cutoff := now.UTC().Add(-r.systemRetention())
	after := ""
	for {
		var n int
		err := r.DMS.Write(ctx, func(t *dms.Service) error {
			rows, err := t.DAL.Runs().List(ctx, runstore.Filter{AfterID: after, CreatedBefore: &cutoff, SystemKeys: keys, SystemOnly: true,
				Statuses: []runstore.Status{runstore.StatusCompleted}, Order: "id", Limit: purgePage})
			if err != nil {
				return err
			}
			n = len(rows)
			ids := make([]string, 0, len(rows))
			for _, row := range rows {
				after = row.ID
				if row.CompletedAt != nil && row.CompletedAt.Before(cutoff) {
					ids = append(ids, row.ID)
				}
			}
			return deleteRuns(ctx, t, ids)
		})
		if err != nil || n < purgePage {
			return err
		}
	}
}

// deleteRuns deletes finished runs: their operations, whose provenance rows,
// steps and staged files go with them, after discarding the staged files a
// failed run kept for a retry. Their tasks stay as a record.
func deleteRuns(ctx context.Context, t *dms.Service, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := t.DiscardRunStagedFiles(ctx, ids...); err != nil {
		return err
	}
	return t.DAL.Operations().Delete(ctx, ids)
}
