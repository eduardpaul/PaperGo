// Package runs owns workflow run records shared by every database driver:
// the runs themselves, the items a selection run covers, the live status
// clients follow and the step results a run checkpoints. Drivers implement
// Queries; this package decides what those statements mean.
package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/live"
	"papergo/internal/dal/refs"
)

// Status is the live status of a run as last observed from its execution.
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

// Run is a stored workflow run. Its identity and inputs never change; only
// its live status does. Empty optional identifiers are stored as NULL.
type Run struct {
	ID, WorkflowID, WorkspaceID, ItemID, EventID, EventType string
	Actor, RetryOf                                          string
	WorkflowVersion, Depth                                  int
	Status                                                  Status
	CreatedAt, StatusAt                                     time.Time
}

// CreateInput describes a run. A missing ID is generated.
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
// selects; manual runs of them are never system runs.
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

// Queries are the statements a driver runs for workflow runs. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	live.Queries
	VersionExists(ctx context.Context, workflowID string, number int64) (bool, error)
	WorkflowRun(ctx context.Context, id string) (Run, error)
	WorkflowRunExists(ctx context.Context, workflowID, eventID string) (bool, error)
	WorkflowRuns(ctx context.Context, f Filter) ([]Run, error)
	InsertWorkflowRun(ctx context.Context, r Run) error
	// SetRunStatus records an observed status, of an active run only when
	// activeOnly, and reports how many runs changed.
	SetRunStatus(ctx context.Context, id string, status Status, at time.Time, activeOnly bool) (int64, error)
	DeleteWorkflowRuns(ctx context.Context, ids []string) error
	// RunItems returns selection items ordered by run and position; empty
	// arguments match every run or item.
	RunItems(ctx context.Context, runIDs []string, itemID string) ([]Item, error)
	InsertRunItem(ctx context.Context, i Item) error
	StepResult(ctx context.Context, runID string, step int) (name string, body []byte, err error)
	InsertStepResult(ctx context.Context, runID string, step int, name string, body []byte) error
	StepBodies(ctx context.Context, runID string) (map[int]json.RawMessage, error)
	// CopyStepResults copies a run's results of the steps before a step to
	// another run.
	CopyStepResults(ctx context.Context, target, source string, before int) error
	DeleteStepResults(ctx context.Context, runIDs []string) error
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

// Create records a queued run of a workflow version in the workflow's
// workspace. Its item must be an item or folder of that workspace, and a
// retry must repeat a run of the same workflow version and workspace.
func (r *Repo) Create(ctx context.Context, in CreateInput) (Run, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	at := now()
	run := Run{
		ID: in.ID, WorkflowID: in.WorkflowID, WorkspaceID: in.WorkspaceID, ItemID: in.ItemID, EventID: in.EventID, EventType: in.EventType,
		Actor: in.Actor, RetryOf: in.RetryOf, WorkflowVersion: in.WorkflowVersion, Depth: in.Depth, Status: StatusQueued, CreatedAt: at, StatusAt: at,
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
	return r.read.WorkflowRun(ctx, id)
}

// Exists reports whether an event already started a run of a workflow.
func (r *Repo) Exists(ctx context.Context, workflowID, eventID string) (bool, error) {
	return r.read.WorkflowRunExists(ctx, workflowID, eventID)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Run, error) {
	if f.SystemOnly && len(f.SystemKeys) == 0 {
		return []Run{}, nil
	}
	return r.read.WorkflowRuns(ctx, f)
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

// Observe records a run's status as observed at a time and notifies clients
// when it changed. With activeOnly, finished runs keep their status.
func (r *Repo) Observe(ctx context.Context, id string, status Status, at time.Time, activeOnly bool) error {
	return r.write(ctx, func(q Queries) error {
		n, err := q.SetRunStatus(ctx, id, status, at.UTC(), activeOnly)
		if err != nil || n == 0 {
			return err
		}
		return live.Notify(ctx, q, live.KindRun, id, live.ActionUpdated)
	})
}

func (r *Repo) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return r.read.DeleteWorkflowRuns(ctx, ids)
}

// Purged reports whether a run was purged with its workspace's content.
func (r *Repo) Purged(ctx context.Context, id string) (bool, error) {
	return r.read.AuditRecorded(ctx, "workflow_run", id, "run.purge")
}

// StepResult returns the name and body a run checkpointed for a step.
func (r *Repo) StepResult(ctx context.Context, runID string, step int) (string, []byte, error) {
	return r.read.StepResult(ctx, runID, step)
}

func (r *Repo) SaveStepResult(ctx context.Context, runID string, step int, name string, body []byte) error {
	return r.read.InsertStepResult(ctx, runID, step, name, body)
}

// StepBodies returns a run's checkpointed bodies by step.
func (r *Repo) StepBodies(ctx context.Context, runID string) (map[int]json.RawMessage, error) {
	return r.read.StepBodies(ctx, runID)
}

// ForkSteps gives a retry the results its origin checkpointed before a step.
func (r *Repo) ForkSteps(ctx context.Context, target, source string, before int) error {
	return r.read.CopyStepResults(ctx, target, source, before)
}

func (r *Repo) DeleteSteps(ctx context.Context, runIDs []string) error {
	if len(runIDs) == 0 {
		return nil
	}
	return r.read.DeleteStepResults(ctx, runIDs)
}
