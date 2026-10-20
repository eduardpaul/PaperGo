package scriptrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"papergo/internal/dal/blobs"
	"papergo/internal/dal/operations"
	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/forms"
	"papergo/internal/httpapi"
	"papergo/internal/script"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

// waitExecution parks a run that waits for operations until its wait is
// released.
const waitExecution = "wait"

// retryAfter is when a slice that could not run, such as during a database
// failure, runs again.
const retryAfter = 30 * time.Second

// Runner executes workflow runs: the script.run operations the dispatcher
// starts. Each claim of a run executes one slice of its script, which
// replays the run's recorded steps and continues until the script returns,
// throws, waits or sleeps.
type Runner struct {
	DMS *dms.Service
	API *httpapi.API
	Log *slog.Logger
	// Slice bounds the JavaScript a slice runs; zero means five seconds.
	Slice time.Duration
	// Retention is how long finished runs and dispatched events are kept;
	// zero means 30 days. SystemRetention is how long successful automatic
	// runs of system built-ins are kept; zero means a day.
	Retention, SystemRetention time.Duration
	engine                     *script.Engine
	wake                       chan struct{}
}

// Register makes s execute runs, and wakes the dispatcher after each commit
// that logged events.
func (r *Runner) Register(s *dms.Service) error {
	r.engine = script.New(&Effects{DMS: r.DMS, API: r.API}, r.Slice)
	r.wake = make(chan struct{}, 1)
	s.OnCommit(r.Wake)
	if err := tasks.Register(s); err != nil {
		return err
	}
	return s.RegisterOperationHandler(dms.OperationKindRun, dms.OperationHandler{
		Scope: "workspace.read", KeepFinished: true,
		Validate: func(context.Context, *dms.Service, string, string, json.RawMessage) error {
			return dms.Invalid("workflow runs start through their workflows")
		},
		Execute: r.slice,
		Failed: func(ctx context.Context, t *dms.Service, op *operations.Operation, cause error) error {
			return ended(ctx, t, op, cause.Error())
		},
		Cancelled: func(ctx context.Context, t *dms.Service, op *operations.Operation) error {
			return ended(ctx, t, op, "")
		},
	})
}

// slice executes one slice of a run outside the writer and records how it
// ended under the run's claim.
func (r *Runner) slice(ctx context.Context, s *dms.Service, op *operations.Operation) error {
	var p workflow.RunPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return err
	}
	if p.StartError != "" {
		return s.Write(ctx, func(t *dms.Service) error { return fail(ctx, t, op, p.StartError) })
	}
	v, err := s.DAL.Workflows().Version(ctx, p.WorkflowID, int64(p.Version))
	if err != nil {
		return err
	}
	def, err := workflow.ParseDefinition(v.Definition)
	if err != nil {
		return err
	}
	out, err := r.engine.Execute(ctx, script.Run{ID: op.ID, Author: op.CreatedBy, Claim: op.Claim, Artifact: workflow.Artifact(v), Context: p.Context()})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return dms.Retry(err, retryAfter)
	}
	return s.Write(ctx, func(t *dms.Service) error {
		if err := t.DAL.Runs().AddLogs(ctx, op.ID, logs(out.Logs)); err != nil {
			return err
		}
		switch {
		case out.Status == script.Completed:
			output, err := checkOutput(def, out.Output)
			if err != nil {
				return fail(ctx, t, op, err.Error())
			}
			return complete(ctx, t, op, p, output)
		case out.Status == script.Failed:
			return fail(ctx, t, op, out.Error)
		case out.Until != nil:
			return t.SleepOperation(ctx, op, *out.Until)
		default:
			// A wait released while the slice ran leaves the run ready.
			waiting, err := t.DAL.Runs().Waiting(ctx, op.ID)
			if err != nil {
				return err
			}
			if !waiting {
				return t.ReleaseOperation(ctx, op)
			}
			return t.ParkOperations(ctx, []*operations.Operation{op}, operations.Parking{ExecutionID: waitExecution})
		}
	})
}

func logs(entries []script.Log) []runstore.Log {
	out := make([]runstore.Log, len(entries))
	at := time.Now().UTC()
	for i, e := range entries {
		out[i] = runstore.Log{Level: e.Level, Message: e.Message, At: at}
	}
	return out
}

// checkOutput checks what a run returned against its workflow's
// output_schema and size bound, and returns it with the schema's defaults.
func checkOutput(def workflow.Definition, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > workflow.MaxRunOutputBytes {
		return nil, fmt.Errorf("the run returned more than %d bytes; return IDs, not content", workflow.MaxRunOutputBytes)
	}
	if def.OutputSchema == nil {
		return raw, nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("the run's output must be an object matching output_schema")
	}
	value, err := forms.Values(def.OutputSchema, value, "output")
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// complete ends a run's operation successfully with its output.
func complete(ctx context.Context, t *dms.Service, op *operations.Operation, p workflow.RunPayload, output json.RawMessage) error {
	completed := time.Now().UTC()
	op.Status, op.Result, op.CompletedAt = operations.StatusSucceeded, output, &completed
	if err := t.SaveOperation(ctx, op); err != nil {
		return err
	}
	var outputs any
	_ = json.Unmarshal(output, &outputs)
	raise(ctx, t, op.ID, p, "completed", map[string]any{"outputs": outputs})
	if err := t.DiscardStagedFiles(ctx, blobs.StagedSelection{RunIDs: []string{op.ID}}); err != nil {
		return err
	}
	return finished(ctx, t, p)
}

// fail ends a run's operation with message: the script threw or broke a
// limit, or the run could not start.
func fail(ctx context.Context, t *dms.Service, op *operations.Operation, message string) error {
	completed := time.Now().UTC()
	op.Status, op.Error, op.CompletedAt = operations.StatusFailed, message, &completed
	if err := t.SaveOperation(ctx, op); err != nil {
		return err
	}
	return ended(ctx, t, op, message)
}

// ended cleans up after a run that did not complete: failed with message,
// or cancelled without one. A failed run keeps its staged files for a
// retry to use; a cancelled one discards them and cancels the work its
// requests started and the operations it waited for, such as tasks.
func ended(ctx context.Context, t *dms.Service, op *operations.Operation, message string) error {
	var p workflow.RunPayload
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return err
	}
	if message != "" {
		raise(ctx, t, op.ID, p, "failed", map[string]any{"error": message})
	} else {
		started, err := t.DAL.Runs().StepOperations(ctx, op.ID)
		if err != nil {
			return err
		}
		if err = t.CancelOperations(ctx, started); err != nil {
			return err
		}
		if err = t.DiscardStagedFiles(ctx, blobs.StagedSelection{RunIDs: []string{op.ID}}); err != nil {
			return err
		}
	}
	return finished(ctx, t, p)
}

// finished lets the next run of a coalescing workflow on a run's item start
// once the run ended. The tasks the run created outlive it.
func finished(ctx context.Context, t *dms.Service, p workflow.RunPayload) error {
	if p.Serial == "" {
		return nil
	}
	execution := workflow.SerialExecution(p.Serial)
	next, err := t.DAL.Operations().List(ctx, operations.Filter{Kinds: []string{dms.OperationKindRun}, ExecutionID: execution, Statuses: []operations.Status{operations.StatusPending}})
	if err != nil || len(next) == 0 {
		return err
	}
	ids := make([]string, len(next))
	for i, op := range next {
		ids[i] = op.ID
	}
	return t.UnparkOperations(ctx, execution, ids, time.Now())
}

// raise raises wf.{key}.{status} for a run that ended, and the role event
// of the role its workflow fills: one level deeper than the event that
// started the run, on its item.
func raise(ctx context.Context, t *dms.Service, runID string, p workflow.RunPayload, status string, data map[string]any) {
	data["run_id"], data["status"], data["workflow_id"] = runID, status, p.WorkflowID
	event := func(typ string) dms.Event {
		return dms.Event{ID: dms.NewEventID(), Type: typ, WorkspaceID: p.WorkspaceID, CollectionID: p.Event.CollectionID, ResourceID: p.ItemID,
			Actor: p.Event.Actor, Data: data, Depth: p.Event.Depth + 1, CauseRunID: runID}
	}
	t.Emit(ctx, event(workflow.WorkflowEvent(p.Key, status)))
	// Followers of a role hear every filler, whichever fills it for the
	// item's collection.
	if p.Role != "" {
		t.Emit(ctx, event(workflow.RoleEvent(p.Role, status)))
	}
}
