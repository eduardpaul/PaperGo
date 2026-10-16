package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"papergo/ent/task"
	"papergo/ent/workflowrun"
	"papergo/internal/dms"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

// A run waiting for a task or another durable wait holds no worker. Await
// marks that wait as parking and cancels its own execution. Once the wait
// completes, the dispatcher resumes the run, which replays its recorded
// steps and continues from the node; the node's result step releases the
// park. A parked run shows as waiting, counts as active for concurrency
// policies, and is kept by retention. Cancelling a parked run closes its
// waits, releases claims it holds, and marks the run cancelled, so a resume
// already under way stops at the waiting node.

// errParked ends the execution of a run that parked.
var errParked = fmt.Errorf("%w: it is waiting", workflow.ErrStopped)

// errCancelled ends the execution of a run cancelled while it was resuming.
var errCancelled = fmt.Errorf("%w: it was cancelled", workflow.ErrStopped)

// Await returns a parked node's completion. A deadline that has already
// passed fails the node without a step, so a replay reaches the same decision.
func (x *durable) Await(node string, park workflow.Park) (json.RawMessage, error) {
	runID, err := dbos.GetWorkflowID(x.ctx)
	if err != nil {
		return nil, err
	}
	if park.Kind == workflow.WaitKindTask {
		return x.awaitTask(node, runID, park.Key)
	}
	return x.awaitWait(node, runID, park)
}

func (x *durable) awaitTask(node, runID, id string) (json.RawMessage, error) {
	for {
		// Not a step: a completed task never changes, so a replay of a
		// resumed run reads the same result and records it as a step below.
		var cancelled bool
		result, err := dms.Read(x.ctx, x.r.dms, func(t *dms.Service) (map[string]any, error) {
			var err error
			if cancelled, err = t.Client.WorkflowRun.Query().Where(workflowrun.IDEQ(runID), workflowrun.LiveStatusEQ("cancelled")).Exist(x.ctx); err != nil {
				return nil, err
			}
			return tasks.Result(x.ctx, t, id)
		})
		if err != nil {
			return nil, err
		}
		if cancelled {
			return nil, x.stop(node, runID, errCancelled)
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
				if err = t.Client.Task.Update().Where(task.IDEQ(id), task.Parked(true)).SetParked(false).Exec(ctx); err != nil {
					return nil, err
				}
				raw, err := json.Marshal(r)
				if err != nil {
					return nil, err
				}
				if err = workflow.CompleteWait(ctx, t, workflow.WaitKindTask, id, json.RawMessage(`{"status":"completed"}`)); err != nil && !errors.Is(err, dms.ErrConflict) {
					return nil, err
				}
				if err = workflow.ReleaseParking(ctx, t, workflow.WaitKindTask, id, runID); err != nil {
					return nil, err
				}
				return raw, nil
			})
		}
		parked := false
		if err = x.r.dms.Write(x.ctx, func(t *dms.Service) error {
			n, err := t.Client.Task.Update().Where(task.IDEQ(id), task.StatusEQ(task.StatusOpen)).SetParked(true).Save(x.ctx)
			if err != nil {
				return err
			}
			parked = n == 1
			if parked {
				return workflow.ParkWait(x.ctx, t, workflow.WaitKindTask, id, runID)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if !parked {
			// Completed meanwhile.
			continue
		}
		return nil, x.stop(node, runID, errParked)
	}
}

func (x *durable) awaitWait(node, runID string, park workflow.Park) (json.RawMessage, error) {
	for {
		cancelled, state, passed, err := x.readWait(runID, park)
		if err != nil {
			return nil, err
		}
		if cancelled {
			return nil, x.stop(node, runID, errCancelled)
		}
		if state == "completed" {
			return x.recordWait(node, runID, park)
		}
		if passed {
			return nil, errors.New("the wait reached its deadline")
		}
		if state == "closed" {
			return nil, errors.New("the wait was closed")
		}
		held := false
		if err = x.r.dms.Write(x.ctx, func(t *dms.Service) error {
			row, err := workflow.GetWait(x.ctx, t, park.Kind, park.Key)
			if err != nil {
				return err
			}
			if row.Status != "open" {
				return nil
			}
			if !park.Deadline.IsZero() && !time.Now().Before(park.Deadline) {
				return workflow.CloseWait(x.ctx, t, park.Kind, park.Key, runID)
			}
			if err = workflow.ParkWait(x.ctx, t, park.Kind, park.Key, runID); err != nil {
				return err
			}
			held = true
			return nil
		}); err != nil {
			return nil, err
		}
		if !held {
			continue
		}
		return nil, x.stop(node, runID, errParked)
	}
}

func (x *durable) readWait(runID string, park workflow.Park) (cancelled bool, state string, passed bool, err error) {
	err = x.r.dms.Write(x.ctx, func(t *dms.Service) error {
		var e error
		if cancelled, e = t.Client.WorkflowRun.Query().Where(workflowrun.IDEQ(runID), workflowrun.LiveStatusEQ("cancelled")).Exist(x.ctx); e != nil {
			return e
		}
		row, e := workflow.GetWait(x.ctx, t, park.Kind, park.Key)
		if e != nil {
			return e
		}
		state = row.Status
		if cancelled || state != "open" || park.Deadline.IsZero() || time.Now().Before(park.Deadline) {
			return nil
		}
		passed = true
		return workflow.CloseWait(x.ctx, t, park.Kind, park.Key, runID)
	})
	return
}

func (x *durable) recordWait(node, runID string, park workflow.Park) (json.RawMessage, error) {
	return stepTx(x.ctx, x.r, node+" result", func(ctx context.Context, t *dms.Service, _ func(dms.Event)) (json.RawMessage, error) {
		row, err := workflow.GetWait(ctx, t, park.Kind, park.Key)
		if err != nil {
			return nil, err
		}
		if row.Status != "completed" {
			return nil, errors.New("the wait is still open")
		}
		if err = workflow.ReleaseParking(ctx, t, park.Kind, park.Key, runID); err != nil {
			return nil, err
		}
		if len(row.Completion) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return row.Completion, nil
	})
}

// stop cancels the run's own execution and ends it with reason.
func (x *durable) stop(node, runID string, reason error) error {
	if err := dbos.CancelWorkflow(x.r.ctx, runID); err != nil {
		return fmt.Errorf("stop run %s: %w", runID, err)
	}
	// A cancelled workflow stops at the start of its next step; the engine
	// keeps it cancelled whatever the run returns.
	_, _ = dbos.RunAsStep(x.ctx, func(context.Context) (string, error) { return "", reason }, dbos.WithStepName(node+" wait"), dbos.WithStepMaxRetries(0))
	return reason
}

// ResumeDecided resumes the parked runs whose task completed. A run that is
// still parking (not cancelled yet) or already resuming is left for a later
// pass; the resumed run releases the park itself.
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
		case dbos.WorkflowStatusPending, dbos.WorkflowStatusEnqueued:
			continue
		case dbos.WorkflowStatusCancelled:
			if _, err = dbos.ResumeWorkflow[workflow.Result](r.ctx, runID, dbos.WithResumeQueue(runQueue)); err != nil {
				return fmt.Errorf("resume run %s: %w", runID, err)
			}
			if err = r.dms.Write(ctx, func(t *dms.Service) error {
				_, err := t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(runID), workflowrun.LiveStatusEQ("waiting")).SetLiveStatus("queued").Save(ctx)
				return err
			}); err != nil {
				return err
			}
		default:
			// The run ended without reaching the task node again.
			if err = r.dms.Write(ctx, func(t *dms.Service) error {
				return t.Client.Task.UpdateOneID(row.ID).SetParked(false).Exec(ctx)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResumeWaits resumes runs parked on a generic wait that has completed,
// including a wait whose deadline passed. Human tasks resume through
// ResumeDecided, so a run is not resumed twice for the same task.
func (r *Runner) ResumeWaits(ctx context.Context) error {
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		return workflow.ExpireWaits(ctx, t, time.Now().UTC())
	}); err != nil {
		return err
	}
	rows, err := dms.Read(ctx, r.dms, func(t *dms.Service) ([]workflow.Wait, error) {
		return workflow.CompletedParking(ctx, t)
	})
	if err != nil || len(rows) == 0 {
		return err
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.RunID
	}
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
	if err != nil {
		return err
	}
	byID := map[string]dbos.WorkflowStatusType{}
	for _, s := range statuses {
		byID[s.ID] = s.Status
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.RunID] {
			continue
		}
		seen[row.RunID] = true
		switch byID[row.RunID] {
		case dbos.WorkflowStatusPending, dbos.WorkflowStatusEnqueued:
			continue
		case dbos.WorkflowStatusCancelled:
			if _, err = dbos.ResumeWorkflow[workflow.Result](r.ctx, row.RunID, dbos.WithResumeQueue(runQueue)); err != nil {
				return fmt.Errorf("resume run %s: %w", row.RunID, err)
			}
			if err = r.dms.Write(ctx, func(t *dms.Service) error {
				_, err := t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(row.RunID), workflowrun.LiveStatusEQ("waiting")).SetLiveStatus("queued").Save(ctx)
				return err
			}); err != nil {
				return err
			}
		default:
			if err = r.dms.Write(ctx, func(t *dms.Service) error {
				return workflow.ReleaseParking(ctx, t, row.Kind, row.Key, row.RunID)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// parkedRuns returns which of the runs are parked on a task or an open wait.
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
	waiting, err := workflow.ParkingRuns(ctx, t, runIDs)
	if err != nil {
		return nil, err
	}
	for id := range waiting {
		out[id] = true
	}
	return out, nil
}

// cancelRun stops a run and cancels its open tasks. A parked run is already
// stopped: its staged files are discarded and it shows as cancelled, which
// also stops a resume under way. Any other run is cancelled in the engine.
func (r *Runner) cancelRun(ctx context.Context, id string) error {
	parked := false
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		var err error
		if parked, err = tasks.CancelRun(ctx, t, id); err != nil {
			return err
		}
		waiting, err := workflow.ParkingRuns(ctx, t, []string{id})
		if err != nil {
			return err
		}
		parked = parked || waiting[id]
		if err = workflow.CloseRunWaits(ctx, t, id); err != nil {
			return err
		}
		if !parked {
			return nil
		}
		if err = t.DiscardRunStagedFiles(ctx, id); err != nil {
			return err
		}
		_, err = t.Client.WorkflowRun.Update().Where(workflowrun.IDEQ(id)).SetLiveStatus("cancelled").Save(ctx)
		return err
	}); err != nil {
		return err
	}
	// A parked run may be resuming already: cancel that execution too.
	return dbos.CancelWorkflow(r.ctx, id)
}
