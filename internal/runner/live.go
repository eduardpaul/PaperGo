package runner

import (
	"context"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"papergo/ent"
	"papergo/ent/workflowrun"
	"papergo/internal/dms"
)

// The runner batches status observation once per dispatch interval, instead of
// asking every streaming client to poll the execution engine independently.
func (r *Runner) SyncLiveRuns(ctx context.Context) error {
	after := ""
	for ctx.Err() == nil {
		rows, err := r.dms.Client.WorkflowRun.Query().Where(workflowrun.LiveStatusIn("queued", "running"), workflowrun.IDGT(after)).Order(ent.Asc(workflowrun.FieldID)).Limit(200).All(ctx)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
		if err != nil {
			return err
		}
		byID := map[string]dbos.WorkflowStatus{}
		for _, entry := range statuses {
			byID[entry.ID] = entry
		}
		err = r.dms.Write(ctx, func(t *dms.Service) error {
			for _, row := range rows {
				entry, exists := byID[row.ID]
				if !exists {
					continue
				}
				state := status(entry.Status)
				at := entry.UpdatedAt.UnixMilli()
				if at < 0 {
					at = 0
				}
				if row.LiveStatus == state && row.LiveUpdatedAt == at {
					continue
				}
				// A source may have been purged after the status read.
				if _, err := t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(row.ID), workflowrun.LiveStatusIn("queued", "running")).SetLiveStatus(state).SetLiveUpdatedAt(at).Save(ctx); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		if len(rows) < 200 {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
	return ctx.Err()
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
