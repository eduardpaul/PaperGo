// Package runs owns workflow run records shared by every database driver:
// the provenance of each run operation, the items a selection run covers,
// and the steps a run's script recorded. Drivers implement
// Queries; this package decides what those statements mean.
package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/refs"
)

// Status is a run's status, derived from its operation.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusRunning, StatusWaiting, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Active are the statuses of runs that have not finished.
var Active = []Status{StatusQueued, StatusRunning, StatusWaiting}

// Finished reports whether a run with the status has ended.
func (s Status) Finished() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// Run is a workflow run: the provenance of a script.run operation, which
// holds its execution. The provenance never changes. Empty optional
// identifiers are stored as NULL.
type Run struct {
	ID, WorkflowID, WorkspaceID, ItemID, EventID, EventType string
	Actor, RetryOf                                          string
	WorkflowVersion, Depth                                  int
	CreatedAt                                               time.Time
	// Status and the rest are the operation's: queued until a slice ran,
	// running while one runs or the run can continue, and waiting while it
	// waits for operations or a time.
	Status                 Status
	Error                  string
	ErrorCode              operations.ErrorCode
	Output                 json.RawMessage
	Attempts               int
	StartedAt, CompletedAt *time.Time
	UpdatedAt              time.Time
	// WaitingUntil is when a run waiting for a time continues.
	WaitingUntil *time.Time
	// ExecutionID is what a waiting run waits under, such as the active run
	// of a coalescing workflow on its item.
	ExecutionID string
	// Logs are what the run's script logged, oldest first.
	Logs []Log
}

// Log is one entry a run's script logged.
type Log struct {
	Level   string    `json:"level"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// MaxLogs bounds the entries kept per run: the oldest go first.
const MaxLogs = 500

// execution fills r from its operation.
func (r *Run) execution(op operations.Operation, now time.Time) {
	r.Error, r.ErrorCode, r.Attempts, r.ExecutionID = op.Error, op.ErrorCode, op.Attempts, op.ExecutionID
	r.StartedAt, r.CompletedAt, r.UpdatedAt = op.StartedAt, op.CompletedAt, op.UpdatedAt
	if op.Status == operations.StatusSucceeded {
		r.Output = op.Result
	}
	switch {
	case op.Status == operations.StatusSucceeded:
		r.Status = StatusCompleted
	case op.Status == operations.StatusFailed:
		r.Status = StatusFailed
	case op.Status == operations.StatusCancelled:
		r.Status = StatusCancelled
	case op.ExecutionID != "":
		r.Status = StatusWaiting
	case op.Claim.Holder != "" && op.Claim.Until.After(now):
		r.Status = StatusRunning
	case op.StartedAt == nil:
		r.Status = StatusQueued
	case op.AttemptAt.After(now):
		r.Status = StatusWaiting
		at := op.AttemptAt
		r.WaitingUntil = &at
	default:
		r.Status = StatusRunning
	}
}

// CreateInput describes a run of the operation ID.
type CreateInput struct {
	ID, WorkflowID, WorkspaceID, ItemID, EventID, EventType string
	Actor, RetryOf                                          string
	WorkflowVersion, Depth                                  int
}

// Item is one item of a selection run, in its selection order.
type Item struct {
	ID, RunID, ItemID string
	Position          int
}

// Filter selects runs, newest first unless Order is "id". SystemKeys names
// the built-in workflows whose runs HideSystem hides and SystemOnly
// selects; manual runs of them are never system runs. Statuses selects by
// derived status.
type Filter struct {
	WorkspaceID, WorkflowID, AfterID, BeforeID string
	// Origin is "manual", "schedule" or "automatic".
	Origin                                      string
	IDs, ItemIDs, SystemKeys, ExcludeEventTypes []string
	Statuses                                    []Status
	HideSystem, SystemOnly                      bool
	CreatedBefore, BeforeCreatedAt              *time.Time
	Limit                                       int
	Order                                       string
}

// operationStatuses are the operation statuses runs of statuses have.
func operationStatuses(statuses []Status) []operations.Status {
	var out []operations.Status
	add := func(s operations.Status) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	for _, s := range statuses {
		switch s {
		case StatusCompleted:
			add(operations.StatusSucceeded)
		case StatusFailed:
			add(operations.StatusFailed)
		case StatusCancelled:
			add(operations.StatusCancelled)
		default:
			add(operations.StatusPending)
		}
	}
	return out
}

// Queries are the statements a driver runs for workflow runs. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	live.Queries
	StepQueries
	Operations(ctx context.Context, f operations.Filter) ([]operations.Operation, error)
	VersionExists(ctx context.Context, workflowID string, number int64) (bool, error)
	WorkflowRun(ctx context.Context, id string) (Run, error)
	WorkflowRunExists(ctx context.Context, workflowID, eventID string) (bool, error)
	// WorkflowRuns returns the runs f selects; operationStatuses limits
	// them to runs whose operations have one of those statuses.
	WorkflowRuns(ctx context.Context, f Filter, operationStatuses []operations.Status) ([]Run, error)
	InsertWorkflowRun(ctx context.Context, r Run) error
	SetRunLogs(ctx context.Context, id string, logs []Log) error
	// RunItems returns selection items ordered by run and position; empty
	// arguments match every run or item.
	RunItems(ctx context.Context, runIDs []string, itemID string) ([]Item, error)
	InsertRunItem(ctx context.Context, i Item) error
	AuditRecorded(ctx context.Context, entityType, entityID, action string) (bool, error)
}

// Repo runs run reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds run rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// Create records the provenance of the run operation in.ID: a run of a
// workflow version in the workflow's workspace. Its item must be an item
// or folder of that workspace, and a retry must repeat a run of the same
// workflow version and workspace.
func (r *Repo) Create(ctx context.Context, in CreateInput) (Run, error) {
	if in.ID == "" {
		return Run{}, fmt.Errorf("%w: a run needs its operation", dberr.ErrInvalidReference)
	}
	at := now()
	run := Run{
		ID: in.ID, WorkflowID: in.WorkflowID, WorkspaceID: in.WorkspaceID, ItemID: in.ItemID, EventID: in.EventID, EventType: in.EventType,
		Actor: in.Actor, RetryOf: in.RetryOf, WorkflowVersion: in.WorkflowVersion, Depth: in.Depth, Status: StatusQueued, CreatedAt: at, UpdatedAt: at,
	}
	err := r.write(ctx, func(q Queries) error {
		wf, err := q.Workflow(ctx, in.WorkflowID)
		if err != nil {
			return err
		}
		if wf.WorkspaceID != in.WorkspaceID {
			return dberr.ErrInvalidReference
		}
		if err := q.LockWorkspace(ctx, in.WorkspaceID); err != nil {
			return err
		}
		ok, err := q.VersionExists(ctx, in.WorkflowID, int64(in.WorkflowVersion))
		if err != nil {
			return err
		}
		if !ok {
			return dberr.ErrInvalidReference
		}
		if in.ItemID != "" {
			if err := selectable(ctx, q, in.ItemID, in.WorkspaceID); err != nil {
				return err
			}
		}
		if in.RetryOf != "" {
			origin, err := q.WorkflowRun(ctx, in.RetryOf)
			if dberr.IsNotFound(err) {
				return fmt.Errorf("%w: retry origin does not exist", dberr.ErrInvalidReference)
			}
			if err != nil {
				return err
			}
			if origin.WorkflowID != in.WorkflowID || origin.WorkspaceID != in.WorkspaceID || origin.WorkflowVersion != in.WorkflowVersion {
				return fmt.Errorf("%w: retry origin must belong to the same workflow version and workspace", dberr.ErrInvalidReference)
			}
		}
		if err := q.InsertWorkflowRun(ctx, run); err != nil {
			return err
		}
		return live.Notify(ctx, q, live.KindRun, run.ID, live.ActionCreated)
	})
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

// selectable checks that a run may cover an item: an item or folder of the
// run's workspace.
func selectable(ctx context.Context, q Queries, itemID, workspaceID string) error {
	item, err := q.Resource(ctx, itemID)
	if err != nil {
		return err
	}
	if item.WorkspaceID != workspaceID || item.Kind != refs.KindItem && item.Kind != refs.KindFolder {
		return dberr.ErrInvalidReference
	}
	return nil
}

func (r *Repo) Find(ctx context.Context, id string) (Run, error) {
	run, err := r.read.WorkflowRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	out, err := r.executions(ctx, []Run{run}, nil)
	if err != nil {
		return Run{}, err
	}
	return out[0], nil
}

// executions fills runs from their operations, keeping those with one of
// statuses when there are any.
func (r *Repo) executions(ctx context.Context, rows []Run, statuses []Status) ([]Run, error) {
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]string, len(rows))
	for i, run := range rows {
		ids[i] = run.ID
	}
	ops, err := r.read.Operations(ctx, operations.Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := make(map[string]operations.Operation, len(ops))
	for _, op := range ops {
		byID[op.ID] = op
	}
	at := now()
	out := rows[:0]
	for _, run := range rows {
		op, ok := byID[run.ID]
		if !ok {
			return nil, fmt.Errorf("run %s has no operation", run.ID)
		}
		run.execution(op, at)
		if len(statuses) == 0 || slices.Contains(statuses, run.Status) {
			out = append(out, run)
		}
	}
	return out, nil
}

// Exists reports whether an event already started a run of a workflow.
func (r *Repo) Exists(ctx context.Context, workflowID, eventID string) (bool, error) {
	return r.read.WorkflowRunExists(ctx, workflowID, eventID)
}

// List returns the runs f selects. A status filter of queued, running or
// waiting is applied after reading the pending runs, so a limited page may
// hold fewer runs.
func (r *Repo) List(ctx context.Context, f Filter) ([]Run, error) {
	if f.SystemOnly && len(f.SystemKeys) == 0 {
		return []Run{}, nil
	}
	rows, err := r.read.WorkflowRuns(ctx, f, operationStatuses(f.Statuses))
	if err != nil {
		return nil, err
	}
	return r.executions(ctx, rows, f.Statuses)
}

// Items returns selection items of runs, of an item, or both.
func (r *Repo) Items(ctx context.Context, runIDs []string, itemID string) ([]Item, error) {
	return r.read.RunItems(ctx, runIDs, itemID)
}

// AddItem adds an item or folder of the run's workspace to its selection.
func (r *Repo) AddItem(ctx context.Context, runID, itemID string, position int) error {
	return r.write(ctx, func(q Queries) error {
		run, err := q.WorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		if err := selectable(ctx, q, itemID, run.WorkspaceID); err != nil {
			return err
		}
		return q.InsertRunItem(ctx, Item{ID: fmt.Sprintf("%s:%d", runID, position), RunID: runID, ItemID: itemID, Position: position})
	})
}

// AddLogs appends what a run's script logged, keeping the last MaxLogs
// entries.
func (r *Repo) AddLogs(ctx context.Context, id string, logs []Log) error {
	if len(logs) == 0 {
		return nil
	}
	return r.write(ctx, func(q Queries) error {
		run, err := q.WorkflowRun(ctx, id)
		if err != nil {
			return err
		}
		all := append(run.Logs, logs...)
		if len(all) > MaxLogs {
			all = all[len(all)-MaxLogs:]
		}
		return q.SetRunLogs(ctx, id, all)
	})
}

// Purged reports whether a run was purged with its workspace's content.
func (r *Repo) Purged(ctx context.Context, id string) (bool, error) {
	return r.read.AuditRecorded(ctx, "workflow_run", id, "run.purge")
}
