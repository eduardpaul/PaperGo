// Package tasks owns task persistence rules shared by every database driver.
// Drivers implement Queries; this package decides what those statements mean.
package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/model"
)

// Task is a stored task. Empty optional identifiers and nil JSON documents are
// stored as NULL.
type Task struct {
	ID, WorkspaceID, ItemID     string
	Kind                        Kind
	Key, Title, Description     string
	Assignees                   []string
	Completion                  Completion
	Outcomes                    []model.TaskOutcome
	InputSchema, Values, Review map[string]any
	Priority                    Priority
	DueAt                       *time.Time
	Source                      Source
	WorkflowID, RunID, Node     string
	Wait                        bool
	Status                      Status
	Outcome, CompletedBy        string
	CompletedAt                 *time.Time
	Comment                     string
	Input                       json.RawMessage
	CreatedBy                   string
	CreatedAt, UpdatedAt        time.Time
	Version                     int64
}

// Response is one assignee entry's answer to a task.
type Response struct {
	ID, TaskID, Subject, Assignee, Outcome, Comment string
	Input                                           json.RawMessage
	CreatedAt                                       time.Time
}

// CreateInput contains the resolved task settings. Authorization, forms and
// staged-file review checks belong to the application transaction that calls it.
type CreateInput struct {
	ID, WorkspaceID, ItemID     string
	Kind                        Kind
	Key, Title, Description     string
	Assignees                   []string
	Completion                  Completion
	Outcomes                    []model.TaskOutcome
	InputSchema, Values, Review map[string]any
	Priority                    Priority
	DueAt                       *time.Time
	Source                      Source
	WorkflowID, RunID, Node     string
	Wait                        bool
	CreatedBy                   string
}

type ResponseInput struct {
	Subject, Assignee, Outcome, Comment string
	Input                               json.RawMessage
}

// Filter selects tasks. Offset applies only together with Limit.
type Filter struct {
	Status                                   Status
	Kind                                     Kind
	Key, WorkspaceID, ItemID, RunID, Subject string
	Groups                                   []string
	AssignedOnly, Waiting, WaitOnly          bool
	Offset, Limit                            int
}

// Queries are the statements a driver runs for tasks. Each method executes on
// the transaction or connection the driver was bound to.
type Queries interface {
	refs.Queries
	// LockTask returns the current task after serializing writers of it and
	// its workspace.
	LockTask(ctx context.Context, id string) (Task, error)
	Task(ctx context.Context, id string) (Task, error)
	Tasks(ctx context.Context, f Filter) ([]Task, error)
	InsertTask(ctx context.Context, t Task) error
	// UpdateTask writes t's mutable state when the stored version is from.
	UpdateTask(ctx context.Context, t Task, from int64) error
	InsertResponse(ctx context.Context, r Response) error
	// Responses returns answers ordered by creation; empty assignees match all.
	Responses(ctx context.Context, taskIDs, assignees []string) ([]Response, error)
}

// Repo runs task reads on its connection and task changes in transactions.
type Repo struct {
	read  Queries
	write func(context.Context, func(Queries) error) error
}

// NewRepo binds task rules to a driver. write runs fn atomically, inside the
// caller's transaction when one is bound.
func NewRepo(read Queries, write func(context.Context, func(Queries) error) error) *Repo {
	return &Repo{read: read, write: write}
}

func (r *Repo) Find(ctx context.Context, id string) (Task, error) {
	return r.read.Task(ctx, id)
}

func (r *Repo) List(ctx context.Context, f Filter) ([]Task, error) {
	return r.read.Tasks(ctx, f)
}

func (r *Repo) Responses(ctx context.Context, taskIDs, assignees []string) ([]Response, error) {
	if len(taskIDs) == 0 {
		return []Response{}, nil
	}
	return r.read.Responses(ctx, taskIDs, assignees)
}

func (r *Repo) Create(ctx context.Context, in CreateInput) (Task, error) {
	if in.Kind == "" {
		in.Kind = KindTask
	}
	if in.Completion == "" {
		in.Completion = CompletionAny
	}
	if in.Priority == "" {
		in.Priority = PriorityNormal
	}
	if !in.Kind.Valid() || !in.Completion.Valid() || !in.Priority.Valid() || !in.Source.Valid() {
		return Task{}, fmt.Errorf("%w: invalid task kind, completion, priority or source", dberr.ErrCheckViolation)
	}
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	var created Task
	err := r.write(ctx, func(q Queries) error {
		if err := q.LockWorkspace(ctx, in.WorkspaceID); err != nil {
			return err
		}
		ws, err := q.Resource(ctx, in.WorkspaceID)
		if err != nil {
			return err
		}
		if ws.Kind != refs.KindWorkspace || ws.Deleted {
			return fmt.Errorf("%w: task requires a live workspace", dberr.ErrInvalidContainment)
		}
		if in.ItemID != "" {
			item, err := q.Resource(ctx, in.ItemID)
			if err != nil {
				return err
			}
			if item.Kind != refs.KindItem || item.Deleted || item.WorkspaceID != in.WorkspaceID {
				return fmt.Errorf("%w: task item must be live and in its workspace", dberr.ErrInvalidContainment)
			}
		}
		if err := validateAssignees(ctx, q, in.Assignees); err != nil {
			return err
		}
		if len(in.Outcomes) < 1 || len(in.Outcomes) > 10 {
			return fmt.Errorf("%w: tasks need 1 to 10 outcomes", dberr.ErrCheckViolation)
		}
		keys := map[string]bool{}
		for _, outcome := range in.Outcomes {
			if outcome.Key == "" || keys[outcome.Key] {
				return fmt.Errorf("%w: task outcomes need distinct keys", dberr.ErrCheckViolation)
			}
			keys[outcome.Key] = true
		}
		if in.Source == SourceWorkflow {
			wf, err := q.Workflow(ctx, in.WorkflowID)
			if err != nil {
				return err
			}
			matches, err := q.RunInWorkflow(ctx, in.RunID, in.WorkflowID, in.WorkspaceID)
			if err != nil {
				return err
			}
			if wf.Deleted || wf.WorkspaceID != in.WorkspaceID || !matches || in.Node == "" {
				return fmt.Errorf("%w: task workflow and run must belong to its workspace", dberr.ErrInvalidReference)
			}
		} else if in.WorkflowID != "" || in.RunID != "" || in.Node != "" || in.Wait {
			return fmt.Errorf("%w: only workflow tasks may have a workflow, run, node or wait", dberr.ErrInvalidReference)
		}
		now := Now()
		created = Task{
			ID: in.ID, WorkspaceID: in.WorkspaceID, ItemID: in.ItemID, Kind: in.Kind, Key: in.Key, Title: in.Title, Description: in.Description,
			Assignees: in.Assignees, Completion: in.Completion, Outcomes: in.Outcomes, InputSchema: in.InputSchema, Values: in.Values, Review: in.Review,
			Priority: in.Priority, DueAt: utc(in.DueAt), Source: in.Source, WorkflowID: in.WorkflowID, RunID: in.RunID, Node: in.Node, Wait: in.Wait,
			Status: StatusOpen, CreatedBy: in.CreatedBy, CreatedAt: now, UpdatedAt: now, Version: 1,
		}
		return q.InsertTask(ctx, created)
	})
	return created, err
}

// Respond records one assignee's answer and chooses any/all completion in the
// same transaction. A stale version or terminal task cannot accept an answer.
func (r *Repo) Respond(ctx context.Context, id string, version int64, in ResponseInput) (Task, error) {
	var result Task
	err := r.write(ctx, func(q Queries) error {
		task, err := mutable(ctx, q, id, version)
		if err != nil {
			return err
		}
		if task.Status != StatusOpen {
			return fmt.Errorf("%w: task is %s", dberr.ErrInvalidStateTransition, task.Status)
		}
		if !slices.Contains(task.Assignees, in.Assignee) {
			return fmt.Errorf("%w: response is not for an assigned entry", dberr.ErrInvalidReference)
		}
		index := slices.IndexFunc(task.Outcomes, func(o model.TaskOutcome) bool { return o.Key == in.Outcome })
		if index < 0 {
			return fmt.Errorf("%w: unknown task outcome", dberr.ErrInvalidReference)
		}
		response := Response{ID: uuid.NewString(), CreatedAt: Now(), TaskID: id, Subject: in.Subject, Assignee: in.Assignee, Outcome: in.Outcome, Comment: in.Comment, Input: in.Input}
		if err := q.InsertResponse(ctx, response); err != nil {
			return err
		}
		completing := &response
		if task.Completion == CompletionAll && !task.Outcomes[index].Decisive {
			if completing, err = settled(ctx, q, task); err != nil {
				return err
			}
		}
		if completing != nil {
			complete(&task, *completing)
		}
		result, err = save(ctx, q, task)
		return err
	})
	return result, err
}

// Reassign replaces assignees and settles an all-mode task when all remaining
// entries have answered. Responses from removed entries stay in its history.
func (r *Repo) Reassign(ctx context.Context, id string, version int64, assignees []string) (Task, error) {
	var result Task
	err := r.write(ctx, func(q Queries) error {
		task, err := mutable(ctx, q, id, version)
		if err != nil {
			return err
		}
		if task.Status != StatusOpen {
			return fmt.Errorf("%w: task is %s", dberr.ErrInvalidStateTransition, task.Status)
		}
		if err := validateAssignees(ctx, q, assignees); err != nil {
			return err
		}
		task.Assignees = assignees
		if task.Completion == CompletionAll {
			completing, err := settled(ctx, q, task)
			if err != nil {
				return err
			}
			if completing != nil {
				complete(&task, *completing)
			}
		}
		result, err = save(ctx, q, task)
		return err
	})
	return result, err
}

// Cancel marks an open task terminal; an already terminal task is unchanged.
func (r *Repo) Cancel(ctx context.Context, id string, version int64) (Task, error) {
	var result Task
	err := r.write(ctx, func(q Queries) error {
		task, err := mutable(ctx, q, id, version)
		if err != nil {
			return err
		}
		if task.Status != StatusOpen {
			result = task
			return nil
		}
		now := Now()
		task.Status, task.CompletedAt = StatusCancelled, &now
		result, err = save(ctx, q, task)
		return err
	})
	return result, err
}

// Now is the timestamp tasks store, at the precision every engine keeps.
func Now() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}

func mutable(ctx context.Context, q Queries, id string, version int64) (Task, error) {
	task, err := q.LockTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Version != version {
		return Task{}, dberr.ErrConcurrentUpdate
	}
	return task, nil
}

func save(ctx context.Context, q Queries, task Task) (Task, error) {
	from := task.Version
	task.Version++
	task.UpdatedAt = Now()
	return task, q.UpdateTask(ctx, task, from)
}

func validateAssignees(ctx context.Context, q Queries, assignees []string) error {
	if len(assignees) < 1 || len(assignees) > 50 {
		return fmt.Errorf("%w: tasks need 1 to 50 assignees", dberr.ErrCheckViolation)
	}
	seen := map[string]bool{}
	for _, assignee := range assignees {
		if seen[assignee] {
			return fmt.Errorf("%w: duplicate task assignee", dberr.ErrCheckViolation)
		}
		seen[assignee] = true
		kind, id := "user", assignee
		if group, ok := strings.CutPrefix(assignee, "group:"); ok {
			kind, id = "group", group
		}
		exists, err := q.PrincipalExists(ctx, kind, id)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: unknown %s principal %q", dberr.ErrInvalidReference, kind, id)
		}
	}
	return nil
}

// settled returns the answer that completes an all-mode task once every
// current entry has answered. Ties choose the first configured outcome.
func settled(ctx context.Context, q Queries, task Task) (*Response, error) {
	rows, err := q.Responses(ctx, []string{task.ID}, task.Assignees)
	if err != nil {
		return nil, err
	}
	if len(rows) < len(task.Assignees) {
		return nil, nil
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Outcome]++
	}
	best, most := "", 0
	for _, outcome := range task.Outcomes {
		if counts[outcome.Key] > most {
			best, most = outcome.Key, counts[outcome.Key]
		}
	}
	for _, row := range slices.Backward(rows) {
		if row.Outcome == best {
			return &row, nil
		}
	}
	return nil, nil
}

func complete(task *Task, response Response) {
	now := Now()
	task.Status = StatusCompleted
	task.Outcome = response.Outcome
	task.CompletedBy = response.Subject
	task.CompletedAt = &now
	task.Comment = response.Comment
	task.Input = response.Input
}
