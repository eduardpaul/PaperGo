// Package workflows owns workflow definitions shared by every database
// driver: the workflows, their immutable versions and the triggers indexed
// from their definitions. Drivers implement Queries; this package decides
// what those statements mean.
package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/jsonutil"
	"papergo/internal/model"
)

// State is where a workflow is in its authoring lifecycle.
type State string

const (
	StateDraft     State = "draft"
	StatePublished State = "published"
	StateArchived  State = "archived"
)

func (s State) Valid() bool {
	return s == StateDraft || s == StatePublished || s == StateArchived
}

// Workflow is a stored workflow. Empty optional identifiers are stored as
// NULL. Parameters keep numeric literals exactly.
type Workflow struct {
	ID, WorkspaceID, Key, Name, Description, CreatedBy, UpdatedBy string
	BuiltinKey, CollectionID, Role                                string
	State                                                         State
	AutomaticEnabled, ManualEnabled, AutomaticProvider            bool
	Parameters                                                    map[string]any
	CurrentVersion, Version                                       int
	CreatedAt, UpdatedAt                                          time.Time
	DeletedAt                                                     *time.Time
}

// Deleted reports whether the workflow was deleted.
func (w Workflow) Deleted() bool { return w.DeletedAt != nil }

// Version is an immutable definition of a workflow.
type Version struct {
	ID, WorkflowID, CreatedBy string
	Number                    int
	Definition                json.RawMessage
	CreatedAt                 time.Time
}

// Trigger is an event type or schedule a workflow's definition listens to,
// optionally within one collection. A schedule has its next occurrence.
type Trigger struct {
	ID, WorkflowID, WorkspaceID, CollectionID, Type string
	Position                                        int
	NextAt                                          *time.Time
	CreatedAt                                       time.Time
}

// CreateInput describes a workflow and its first version. A missing ID is
// generated, a missing state is published and missing parameters and
// definition are empty objects.
type CreateInput struct {
	ID, WorkspaceID, CollectionID, Key, Name, Description, Role, BuiltinKey string
	State                                                                   State
	AutomaticEnabled, ManualEnabled, AutomaticProvider                      bool
	Parameters, Definition                                                  json.RawMessage
	CreatedBy                                                               string
}

// Changes names the mutable settings of a revision; nil leaves a setting.
// Identity and scope never change.
type Changes struct {
	Name, Description                                  *string
	State                                              *State
	AutomaticEnabled, ManualEnabled, AutomaticProvider *bool
	Parameters                                         *map[string]any
	// Author records a new version when the subject did not write the
	// current one, so runs act as the subject even if the definition is
	// unchanged.
	Author bool
}

// Filter selects workflows, ordered by ID unless Order is "name" or
// "provider" (enabled providers first).
type Filter struct {
	IDs, BuiltinKeys                                                  []string
	WorkspaceID, Key, Name, BuiltinKey, Role, CollectionID, ExcludeID string
	State                                                             State
	LiveOnly, OnlyBuiltins, CollectionAbsent                          bool
	AutomaticEnabled, AutomaticProvider                               *bool
	Order                                                             string
}

// TriggerInput is a trigger indexed from a definition.
type TriggerInput struct {
	CollectionID, Type string
	Position           int
	NextAt             *time.Time
}

// TriggerFilter selects triggers. MatchCollection selects triggers of the
// collection or of no collection; DueAt selects schedules due by then,
// soonest first.
type TriggerFilter struct {
	Type, WorkflowID, WorkspaceID, CollectionID string
	MatchCollection                             bool
	DueAt                                       *time.Time
	Limit                                       int
}

// Queries are the statements a driver runs for workflows. Each method
// executes on the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	FindWorkflow(ctx context.Context, id string) (Workflow, error)
	Workflows(ctx context.Context, f Filter) ([]Workflow, error)
	// WorkflowProviderExists reports whether a workspace's collection has
	// the built-in workflow or an automatic provider of the role.
	WorkflowProviderExists(ctx context.Context, workspaceID, collectionID, builtinKey, role string) (bool, error)
	InsertWorkflow(ctx context.Context, w Workflow) error
	// UpdateWorkflow writes w's mutable state when the stored version is
	// from.
	UpdateWorkflow(ctx context.Context, w Workflow, from int64) error
	// WorkflowVersions returns a workflow's versions, newest first.
	WorkflowVersions(ctx context.Context, workflowID string) ([]Version, error)
	WorkflowVersion(ctx context.Context, workflowID string, number int64) (Version, error)
	InsertWorkflowVersion(ctx context.Context, v Version) error
	WorkflowTriggers(ctx context.Context, f TriggerFilter) ([]Trigger, error)
	// TriggeredWorkflows returns the live workflows whose current definition
	// has a trigger in a collection, manual and disabled ones included.
	TriggeredWorkflows(ctx context.Context, collectionID string) ([]string, error)
	InsertWorkflowTrigger(ctx context.Context, t Trigger) error
	DeleteWorkflowTriggers(ctx context.Context, workflowID string) error
	SetTriggerNext(ctx context.Context, id string, next time.Time) error
}

// Repo runs workflow reads and single statements on its connection and
// multi-step changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds workflow rules to a driver. write runs fn atomically, inside
// the caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func (r *Repo) Find(ctx context.Context, id string) (Workflow, error) {
	return r.read.FindWorkflow(ctx, id)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Workflow, error) {
	return r.read.Workflows(ctx, f)
}

func (r *Repo) Exists(ctx context.Context, f Filter) (bool, error) {
	rows, err := r.read.Workflows(ctx, f)
	return len(rows) > 0, err
}

// HasProvider reports whether a workspace's collection has the built-in
// workflow, or an automatic provider of the role, deleted or not.
func (r *Repo) HasProvider(ctx context.Context, workspaceID, collectionID, builtinKey, role string) (bool, error) {
	return r.read.WorkflowProviderExists(ctx, workspaceID, collectionID, builtinKey, role)
}

// Versions returns a workflow's versions, newest first.
func (r *Repo) Versions(ctx context.Context, id string) ([]Version, error) {
	return r.read.WorkflowVersions(ctx, id)
}

func (r *Repo) Version(ctx context.Context, id string, number int64) (Version, error) {
	return r.read.WorkflowVersion(ctx, id, number)
}

// collection checks that a workflow may scope to a collection: a live list
// or library of its workspace.
func collection(ctx context.Context, q Queries, id, workspaceID string) error {
	c, err := q.Resource(ctx, id)
	if err != nil {
		return err
	}
	if c.WorkspaceID != workspaceID || c.Kind != refs.KindList && c.Kind != refs.KindLibrary || c.Deleted {
		return dberr.ErrInvalidReference
	}
	return nil
}

// Create records a workflow in a live workspace, optionally scoped to one of
// its lists or libraries, with its first version.
func (r *Repo) Create(ctx context.Context, in CreateInput) (Workflow, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if in.State == "" {
		in.State = StatePublished
	}
	if !in.State.Valid() {
		return Workflow{}, fmt.Errorf("%w: invalid workflow state %q", dberr.ErrCheckViolation, in.State)
	}
	params := model.JSONMap{}
	if len(in.Parameters) > 0 {
		if err := json.Unmarshal(in.Parameters, &params); err != nil || params == nil {
			return Workflow{}, fmt.Errorf("%w: workflow parameters must be an object", dberr.ErrCheckViolation)
		}
	}
	def := in.Definition
	if len(def) == 0 {
		def = json.RawMessage(`{}`)
	}
	at := now()
	w := Workflow{
		ID: in.ID, WorkspaceID: in.WorkspaceID, Key: in.Key, Name: in.Name, Description: in.Description, CreatedBy: in.CreatedBy, UpdatedBy: in.CreatedBy,
		BuiltinKey: in.BuiltinKey, CollectionID: in.CollectionID, Role: in.Role, State: in.State,
		AutomaticEnabled: in.AutomaticEnabled, ManualEnabled: in.ManualEnabled, AutomaticProvider: in.AutomaticProvider,
		Parameters: params, CurrentVersion: 1, Version: 1, CreatedAt: at, UpdatedAt: at,
	}
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, in.WorkspaceID); err != nil {
			return err
		}
		ws, err := q.Resource(ctx, in.WorkspaceID)
		if err != nil && !dberr.IsNotFound(err) {
			return err
		}
		if err != nil || ws.Kind != refs.KindWorkspace || ws.Deleted {
			return fmt.Errorf("%w: workflow workspace must be a workspace", dberr.ErrInvalidContainment)
		}
		if in.CollectionID != "" {
			if err := collection(ctx, q, in.CollectionID, in.WorkspaceID); err != nil {
				return fmt.Errorf("%w: workflow collection must be a list or library of its workspace", dberr.ErrInvalidContainment)
			}
		}
		if err := q.InsertWorkflow(ctx, w); err != nil {
			return err
		}
		return q.InsertWorkflowVersion(ctx, Version{ID: uuid.NewString(), WorkflowID: w.ID, CreatedBy: in.CreatedBy, Number: 1, Definition: def, CreatedAt: at})
	})
	if err != nil {
		return Workflow{}, err
	}
	return w, nil
}

// lock returns the current workflow after serializing writers of its
// workspace.
func lock(ctx context.Context, q Queries, id string) (Workflow, error) {
	w, err := q.FindWorkflow(ctx, id)
	if err != nil {
		return Workflow{}, err
	}
	if err := q.LockWorkspace(ctx, w.WorkspaceID); err != nil {
		return Workflow{}, err
	}
	return q.FindWorkflow(ctx, id)
}

// Revise applies changes to a live workflow read at version and returns it
// with its current definition. A definition that differs from the current one
// becomes the next version.
func (r *Repo) Revise(ctx context.Context, id string, version int, subject string, definition json.RawMessage, in Changes) (Workflow, Version, error) {
	var w Workflow
	var current Version
	err := r.write(ctx, func(q Queries) error {
		var err error
		if w, err = lock(ctx, q, id); err != nil {
			return err
		}
		if w.Version != version {
			return dberr.ErrConcurrentUpdate
		}
		if w.Deleted() {
			return dberr.ErrImmutable
		}
		if current, err = q.WorkflowVersion(ctx, id, int64(w.CurrentVersion)); err != nil {
			return err
		}
		at := now()
		from := int64(w.Version)
		w.Version++
		w.UpdatedAt, w.UpdatedBy = at, subject
		if in.Name != nil {
			w.Name = *in.Name
		}
		if in.Description != nil {
			w.Description = *in.Description
		}
		if in.State != nil {
			if !in.State.Valid() {
				return fmt.Errorf("%w: invalid workflow state %q", dberr.ErrCheckViolation, *in.State)
			}
			w.State = *in.State
		}
		if in.AutomaticEnabled != nil {
			w.AutomaticEnabled = *in.AutomaticEnabled
		}
		if in.ManualEnabled != nil {
			w.ManualEnabled = *in.ManualEnabled
		}
		if in.AutomaticProvider != nil {
			w.AutomaticProvider = *in.AutomaticProvider
		}
		if in.Parameters != nil {
			w.Parameters = *in.Parameters
		}
		if !jsonutil.Equal(current.Definition, definition) || in.Author && current.CreatedBy != subject {
			current = Version{ID: uuid.NewString(), WorkflowID: id, CreatedBy: subject, Number: w.CurrentVersion + 1, Definition: definition, CreatedAt: at}
			if err := q.InsertWorkflowVersion(ctx, current); err != nil {
				return err
			}
			w.CurrentVersion = current.Number
		}
		return q.UpdateWorkflow(ctx, w, from)
	})
	if err != nil {
		return Workflow{}, Version{}, err
	}
	return w, current, nil
}

// Delete deletes a live workflow read at version and removes its triggers.
// Its versions and runs remain.
func (r *Repo) Delete(ctx context.Context, id string, version int, subject string) error {
	return r.write(ctx, func(q Queries) error {
		w, err := lock(ctx, q, id)
		if err != nil {
			return err
		}
		if w.Version != version || w.Deleted() {
			return dberr.ErrConcurrentUpdate
		}
		at := now()
		w.DeletedAt, w.UpdatedAt, w.UpdatedBy = &at, at, subject
		w.Version++
		if err := q.UpdateWorkflow(ctx, w, int64(version)); err != nil {
			return err
		}
		return q.DeleteWorkflowTriggers(ctx, id)
	})
}

// Retire disables and deletes the live workflows a deleted workspace or
// collection leaves behind: every workflow of the workspace, or those of
// the collection and those its current definition triggers in it.
func (r *Repo) Retire(ctx context.Context, workspaceID, collectionID, subject string) error {
	return r.write(ctx, func(q Queries) error {
		var retired []Workflow
		if workspaceID != "" {
			rows, err := q.Workflows(ctx, Filter{WorkspaceID: workspaceID, LiveOnly: true})
			if err != nil {
				return err
			}
			retired = rows
		}
		if collectionID != "" {
			own, err := q.Workflows(ctx, Filter{CollectionID: collectionID, LiveOnly: true})
			if err != nil {
				return err
			}
			ids, err := q.TriggeredWorkflows(ctx, collectionID)
			if err != nil {
				return err
			}
			triggered := []Workflow{}
			if len(ids) > 0 {
				if triggered, err = q.Workflows(ctx, Filter{IDs: ids, LiveOnly: true}); err != nil {
					return err
				}
			}
			seen := map[string]bool{}
			for _, w := range append(own, triggered...) {
				if !seen[w.ID] {
					seen[w.ID] = true
					retired = append(retired, w)
				}
			}
		}
		at := now()
		for _, w := range retired {
			from := w.Version
			w.AutomaticEnabled, w.ManualEnabled = false, false
			w.DeletedAt, w.UpdatedAt, w.UpdatedBy = &at, at, subject
			w.Version++
			if err := q.UpdateWorkflow(ctx, w, int64(from)); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceTriggers indexes a workflow's triggers from its definition. A
// deleted workflow can only lose its triggers.
func (r *Repo) ReplaceTriggers(ctx context.Context, id string, in []TriggerInput) error {
	return r.write(ctx, func(q Queries) error {
		w, err := lock(ctx, q, id)
		if err != nil {
			return err
		}
		if len(in) > 0 && w.Deleted() {
			return dberr.ErrImmutable
		}
		if err := q.DeleteWorkflowTriggers(ctx, id); err != nil {
			return err
		}
		at := now()
		for _, entry := range in {
			if entry.CollectionID != "" {
				if err := collection(ctx, q, entry.CollectionID, w.WorkspaceID); err != nil {
					return err
				}
			}
			var next *time.Time
			if entry.NextAt != nil {
				n := entry.NextAt.UTC()
				next = &n
			}
			t := Trigger{ID: uuid.NewString(), WorkflowID: id, WorkspaceID: w.WorkspaceID, CollectionID: entry.CollectionID, Type: entry.Type, Position: entry.Position, NextAt: next, CreatedAt: at}
			if err := q.InsertWorkflowTrigger(ctx, t); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repo) Triggers(ctx context.Context, f TriggerFilter) ([]Trigger, error) {
	return r.read.WorkflowTriggers(ctx, f)
}

// AdvanceTrigger moves a schedule to its next occurrence.
func (r *Repo) AdvanceTrigger(ctx context.Context, id string, next time.Time) error {
	return r.read.SetTriggerNext(ctx, id, next.UTC())
}
