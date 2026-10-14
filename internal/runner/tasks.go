package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/ent/task"
	"papergo/ent/workflowrun"
	"papergo/internal/dms"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

// A run waiting for a task holds no worker. While the task is open, Await
// parks the run: it marks the task parked and cancels its own execution.
// Once the task completes, ResumeDecided resumes the run, which replays its
// recorded steps and continues from the task node. A parked run shows as
// waiting, counts as active for concurrency policies, and is kept by
// retention.

// errParked ends the execution of a run that parked.
var errParked = errors.New("the run waits for a task")

// Await returns a task node's output once its task is completed.
func (x *durable) Await(node, id string) (json.RawMessage, error) {
	for {
		// Not a step: a completed task never changes, so a replay of a
		// resumed run reads the same result and records it as a step below.
		result, err := dms.Read(x.ctx, x.r.dms, func(t *dms.Service) (map[string]any, error) { return tasks.Result(x.ctx, t, id) })
		if err != nil {
			return nil, err
		}
		if result != nil {
			return stepTx(x.ctx, x.r, node+" result", func(ctx context.Context, t *dms.Service, _ func(dms.Event)) (json.RawMessage, error) {
				r, err := tasks.Result(ctx, t, id)
				if err == nil && r == nil {
					err = errors.New("the task is still open")
				}
				if err != nil {
					return nil, err
				}
				return json.Marshal(r)
			})
		}
		parked := false
		if err = x.r.dms.Write(x.ctx, func(t *dms.Service) error {
			n, err := t.Client.Task.Update().Where(task.IDEQ(id), task.StatusEQ(task.StatusOpen)).SetParked(true).Save(x.ctx)
			parked = n == 1
			return err
		}); err != nil {
			return nil, err
		}
		if !parked {
			// Completed meanwhile.
			continue
		}
		runID, err := dbos.GetWorkflowID(x.ctx)
		if err != nil {
			return nil, err
		}
		if err = dbos.CancelWorkflow(x.r.ctx, runID); err != nil {
			return nil, fmt.Errorf("park run %s: %w", runID, err)
		}
		// A cancelled workflow stops at the start of its next step.
		if _, err = dbos.RunAsStep(x.ctx, func(context.Context) (string, error) { return "", errParked }, dbos.WithStepName(node+" wait"), dbos.WithStepMaxRetries(0)); err == nil {
			err = errParked
		}
		return nil, err
	}
}

// ResumeDecided resumes the parked runs whose task completed. A run that is
// still parking (not cancelled yet) is resumed on a later pass.
func (r *Runner) ResumeDecided(ctx context.Context) error {
	rows, err := r.dms.Client.Task.Query().Where(task.Parked(true), task.StatusIn(task.StatusCompleted, task.StatusExpired)).Limit(100).All(ctx)
	if err != nil || len(rows) == 0 {
		return err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = *row.RunID
	}
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
	if err != nil {
		return err
	}
	byID := map[string]dbos.WorkflowStatusType{}
	for _, s := range statuses {
		byID[s.ID] = s.Status
	}
	for _, row := range rows {
		runID := *row.RunID
		switch byID[runID] {
		case dbos.WorkflowStatusPending:
			// Still parking; resumed on a later pass.
			continue
		case dbos.WorkflowStatusCancelled:
			if _, err = dbos.ResumeWorkflow[workflow.Result](r.ctx, runID, dbos.WithResumeQueue(runQueue)); err != nil {
				return fmt.Errorf("resume run %s: %w", runID, err)
			}
		}
		if err = r.dms.Write(ctx, func(t *dms.Service) error {
			if err := t.Client.Task.UpdateOneID(row.ID).SetParked(false).Exec(ctx); err != nil {
				return err
			}
			_, err := t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(runID), workflowrun.LiveStatusEQ("waiting")).SetLiveStatus("queued").Save(ctx)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// parkedRuns returns which of the runs are parked on a task.
func parkedRuns(ctx context.Context, t *dms.Service, runIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := t.Client.Task.Query().Where(task.RunIDIn(runIDs...), task.Parked(true)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[*row.RunID] = true
	}
	return out, nil
}

// cancelRun stops a run and cancels its open tasks. A parked run is already
// stopped: its staged files are discarded and it shows as cancelled. Any
// other run is cancelled in the engine.
func (r *Runner) cancelRun(ctx context.Context, id string) error {
	parked := false
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		var err error
		if parked, err = tasks.CancelRun(ctx, t, id); err != nil || !parked {
			return err
		}
		if err = t.DiscardRunStagedFiles(ctx, id); err != nil {
			return err
		}
		_, err = t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(id)).SetLiveStatus("cancelled").Save(ctx)
		return err
	}); err != nil {
		return err
	}
	if parked {
		return nil
	}
	return dbos.CancelWorkflow(r.ctx, id)
}
