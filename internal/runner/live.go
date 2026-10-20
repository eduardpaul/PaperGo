package runner

import (
	"context"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

// liveBatch is how many active runs one status observation reads at a time.
const liveBatch = 200

// SyncLiveRuns records the execution engine's status of every active run, so
// streaming clients and status filters read it without polling the engine.
// It returns how many active runs it observed.
func (r *Runner) SyncLiveRuns(ctx context.Context) (int, error) {
	after, observed := "", 0
	for ctx.Err() == nil {
		rows, err := r.dms.DAL.Runs().List(ctx, runstore.Filter{Statuses: runstore.Active, AfterID: after, Order: "id", Limit: liveBatch})
		if err != nil {
			return observed, err
		}
		if len(rows) == 0 {
			return observed, nil
		}
		observed += len(rows)
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		// DBOS's table directly: status and change time are all this needs.
		statuses, err := r.durable.Statuses(ctx, nil, ids)
		if err != nil {
			return observed, err
		}
		parked, err := parkedRuns(ctx, r.dms, ids)
		if err != nil {
			return observed, err
		}
		type change struct {
			id    string
			state runstore.Status
			at    time.Time
		}
		var changes []change
		for _, row := range rows {
			entry, exists := statuses[row.ID]
			if !exists {
				continue
			}
			state := status(dbos.WorkflowStatusType(entry.Status))
			if parked[row.ID] && (state == runstore.StatusRunning || state == runstore.StatusQueued) {
				state = runstore.StatusWaiting
			}
			if row.Status != state || !row.StatusAt.Equal(entry.UpdatedAt) {
				changes = append(changes, change{row.ID, state, entry.UpdatedAt})
			}
		}
		if len(changes) > 0 {
			err = r.dms.Write(ctx, func(t *dms.Service) error {
				for _, c := range changes {
					// A source may have been purged after the status read.
					if err := t.DAL.Runs().Observe(ctx, c.id, c.state, c.at, true); err != nil {
						return err
					}
					if c.state.Finished() {
						if err := workflow.CloseRunWaits(ctx, t, c.id); err != nil {
							return err
						}
					}
					// A finished or cancelled run promotes nothing more. A failed
					// run keeps its staged files for a retry until it is purged.
					if c.state == runstore.StatusCompleted || c.state == runstore.StatusCancelled {
						if err := t.DiscardRunStagedFiles(ctx, c.id); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				return observed, err
			}
		}
		if len(rows) < liveBatch {
			return observed, nil
		}
		after = rows[len(rows)-1].ID
	}
	return observed, ctx.Err()
}

// RunUpdate emits the same authorized run status as the REST feed, without step outputs.
func (r *Runner) RunUpdate(ctx context.Context, subject, id string) (Run, error) {
	row, err := r.row(ctx, subject, id)
	if err != nil {
		return Run{}, err
	}
	runs := []Run{toRun(row)}
	err = r.attach(runs, false)
	return runs[0], err
}
