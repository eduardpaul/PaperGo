package tasks

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"papergo/internal/dal/dberr"
	taskstore "papergo/internal/dal/tasks"

	"papergo/internal/dms"
	"papergo/internal/model"
)

// View is a task as the API shows it.
type View struct {
	ID          string               `json:"id"`
	WorkspaceID string               `json:"workspace_id"`
	ItemID      *string              `json:"item_id,omitempty"`
	Kind        taskstore.Kind       `json:"kind"`
	Key         string               `json:"key,omitempty"`
	Title       string               `json:"title"`
	Description string               `json:"description,omitempty"`
	Assignees   []string             `json:"assignees"`
	Completion  taskstore.Completion `json:"completion"`
	Outcomes    []model.TaskOutcome  `json:"outcomes"`
	InputSchema map[string]any       `json:"input_schema,omitempty"`
	Values      map[string]any       `json:"values,omitempty"`
	Review      map[string]any       `json:"review,omitempty"`
	Priority    taskstore.Priority   `json:"priority"`
	DueAt       *time.Time           `json:"due_at,omitempty"`
	Source      taskstore.Source     `json:"source"`
	WorkflowID  *string              `json:"workflow_id,omitempty"`
	RunID       *string              `json:"run_id,omitempty"`
	Node        *string              `json:"node,omitempty"`
	Status      taskstore.Status     `json:"status"`
	Outcome     *string              `json:"outcome,omitempty"`
	CompletedBy *string              `json:"completed_by,omitempty"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
	Comment     string               `json:"comment,omitempty"`
	Input       json.RawMessage      `json:"input,omitempty"`
	CreatedBy   string               `json:"created_by"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Version     int                  `json:"version"`
	// Responses are the answers so far, oldest first; only in single reads.
	Responses []ResponseView `json:"responses,omitempty"`
	// Answered tells the reader that they, or their group, already answered;
	// one assigned several ways may still answer for the others.
	Answered bool `json:"answered,omitempty"`
	// CanRespond tells the reader whether they may answer now.
	CanRespond bool `json:"can_respond"`
	// CanReassign tells the reader of an open task whether they may change
	// its assignees; set when one task is read.
	CanReassign bool `json:"can_reassign,omitempty"`
}

// ResponseView is an answer as the API shows it.
type ResponseView struct {
	Subject   string          `json:"subject"`
	Assignee  string          `json:"assignee"`
	Outcome   string          `json:"outcome"`
	Comment   string          `json:"comment,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

func toView(tk taskstore.Task) View {
	return View{ID: tk.ID, WorkspaceID: tk.WorkspaceID, ItemID: ptr(tk.ItemID), Kind: tk.Kind, Key: tk.Key, Title: tk.Title, Description: tk.Description,
		Assignees: tk.Assignees, Completion: tk.Completion, Outcomes: tk.Outcomes, InputSchema: tk.InputSchema, Values: tk.Values, Review: tk.Review,
		Priority: tk.Priority, DueAt: tk.DueAt, Source: tk.Source, WorkflowID: ptr(tk.WorkflowID), RunID: ptr(tk.RunID), Node: ptr(tk.Node), Status: tk.Status, Outcome: ptr(tk.Outcome), CompletedBy: ptr(tk.CompletedBy), CompletedAt: tk.CompletedAt, Comment: tk.Comment, Input: tk.Input,
		CreatedBy: tk.CreatedBy, CreatedAt: tk.CreatedAt, UpdatedAt: tk.UpdatedAt, Version: int(tk.Version)}
}

// ptr shows an optional identifier, empty when the task has none.
func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Filter selects tasks.
type Filter struct {
	// Status is open, completed, cancelled or expired; empty is all.
	Status      taskstore.Status
	Kind        taskstore.Kind
	Key         string
	WorkspaceID string
	ItemID      string
	// Waiting keeps the open tasks the reader may still answer.
	Waiting bool
	Limit   int
	// After continues from a previous page's NextCursor.
	After string
}

// maxScans bounds the rows a page reads past tasks the reader cannot see;
// a page cut short still links to the next.
const maxScans = 10

func (f Filter) apply(in taskstore.Filter, subject string, groups []string) (taskstore.Filter, error) {
	if f.Waiting {
		f.Status = taskstore.StatusOpen
	}
	if f.Status != "" && !f.Status.Valid() {
		return taskstore.Filter{}, dms.Invalid("status is open, completed, cancelled or expired")
	}
	if f.Kind != "" && !f.Kind.Valid() {
		return taskstore.Filter{}, dms.Invalid("kind is task or approval")
	}
	in.Status, in.Kind, in.Key, in.WorkspaceID, in.ItemID = f.Status, f.Kind, f.Key, f.WorkspaceID, f.ItemID
	in.Subject, in.Groups, in.Waiting = subject, groups, f.Waiting
	return in, nil
}

// page reads a page of the tasks q selects, keeping those visible says the
// reader may see (all when visible is nil). The cursor counts the rows read.
func page(ctx context.Context, t *dms.Service, q taskstore.Filter, f Filter, subject string, groups []string,
	visible func([]taskstore.Task) (map[string]bool, error)) (dms.Page[View], error) {
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset := 0
	if f.After != "" {
		n, err := strconv.Atoi(f.After)
		if err != nil || n < 0 {
			return dms.Page[View]{}, dms.Invalid("$skiptoken is not a cursor of this list")
		}
		offset = n
	}
	out := dms.Page[View]{Data: []View{}}
	for scans := 1; ; scans++ {
		query := q
		query.Offset, query.Limit = offset, limit+1
		rows, err := t.DAL.Tasks().List(ctx, query)
		if err != nil {
			return out, err
		}
		more := len(rows) > limit
		if more {
			rows = rows[:limit]
		}
		var keep map[string]bool
		if visible != nil {
			if keep, err = visible(rows); err != nil {
				return out, err
			}
		}
		answered, err := answersOf(ctx, t, rows)
		if err != nil {
			return out, err
		}
		for i, tk := range rows {
			if keep != nil && !keep[tk.ID] {
				continue
			}
			if len(out.Data) == limit {
				out.NextCursor = strconv.Itoa(offset + i)
				return out, nil
			}
			out.Data = append(out.Data, viewOf(tk, subject, groups, answered[tk.ID]))
		}
		offset += len(rows)
		if !more {
			return out, nil
		}
		if len(out.Data) == limit || scans == maxScans {
			out.NextCursor = strconv.Itoa(offset)
			return out, nil
		}
	}
}

// answersOf returns the entries that answered each task.
func answersOf(ctx context.Context, t *dms.Service, rows []taskstore.Task) (map[string][]string, error) {
	out := map[string][]string{}
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, len(rows))
	for i, tk := range rows {
		ids[i] = tk.ID
	}
	answers, err := t.DAL.Tasks().Responses(ctx, ids, nil)
	if err != nil {
		return nil, err
	}

	for _, a := range answers {
		out[a.TaskID] = append(out[a.TaskID], a.Assignee)
	}
	return out, nil
}

// reviewable reports which tasks subject may still review, as CanReview
// does for each, in two queries.
func reviewable(ctx context.Context, t *dms.Service, subject string, rows []taskstore.Task) (map[string]bool, error) {
	var workspaces, items []string
	for _, tk := range rows {
		workspaces = append(workspaces, tk.WorkspaceID)
		if tk.ItemID != "" {
			items = append(items, tk.ItemID)
		}
	}
	readable, err := t.AuthorizeMany(ctx, subject, "read", workspaces)
	if err != nil {
		return nil, err
	}
	drafts, err := t.AuthorizeMany(ctx, subject, "read_draft", items)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, tk := range rows {
		out[tk.ID] = readable[tk.WorkspaceID] && (tk.ItemID == "" || drafts[tk.ItemID])
	}
	return out, nil
}

// Service reads and writes tasks for the API, each call in its own
// transaction.
type Service struct {
	DMS *dms.Service
}

// Mine lists the tasks assigned to subject or their groups. Lost access
// hides a task without changing it.
func (s *Service) Mine(ctx context.Context, subject string, f Filter) (dms.Page[View], error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (dms.Page[View], error) {
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return dms.Page[View]{}, err
		}
		q, err := f.apply(taskstore.Filter{AssignedOnly: true}, subject, groups)
		if err != nil {
			return dms.Page[View]{}, err
		}
		return page(ctx, t, q, f, subject, groups, func(rows []taskstore.Task) (map[string]bool, error) { return reviewable(ctx, t, subject, rows) })
	})
}

// Workspace lists a workspace's tasks to its owners.
func (s *Service) Workspace(ctx context.Context, subject, workspaceID string, f Filter) (dms.Page[View], error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (dms.Page[View], error) {
		if !manages(ctx, t, subject, workspaceID) {
			if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
				return dms.Page[View]{}, err
			}
			return dms.Page[View]{}, dms.ErrForbidden
		}
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return dms.Page[View]{}, err
		}
		f.WorkspaceID = workspaceID
		q, err := f.apply(taskstore.Filter{}, subject, groups)
		if err != nil {
			return dms.Page[View]{}, err
		}
		return page(ctx, t, q, f, subject, groups, nil)
	})
}

// Item lists the tasks about an item that subject may see: those assigned
// to them, those they created, or all to the workspace's owners.
func (s *Service) Item(ctx context.Context, subject, itemID string, f Filter) (dms.Page[View], error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (dms.Page[View], error) {
		item, err := t.Authorize(ctx, subject, itemID, "read_draft")
		if err != nil {
			return dms.Page[View]{}, err
		}
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return dms.Page[View]{}, err
		}
		f.ItemID = itemID
		q, err := f.apply(taskstore.Filter{}, subject, groups)
		if err != nil {
			return dms.Page[View]{}, err
		}
		owner := manages(ctx, t, subject, item.WorkspaceID)
		_, err = t.Authorize(ctx, subject, item.WorkspaceID, "read")
		member := err == nil
		return page(ctx, t, q, f, subject, groups, func(rows []taskstore.Task) (map[string]bool, error) {
			out := map[string]bool{}
			for _, tk := range rows {
				out[tk.ID] = owner || member && (tk.CreatedBy == subject || len(entriesOf(tk, subject, groups)) > 0)
			}
			return out, nil
		})
	})
}

// Get returns a task with its responses to whoever may see it.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (View, error) {
		tk, err := t.DAL.Tasks().Find(ctx, id)
		if dberr.IsNotFound(err) {
			return View{}, dms.ErrNotFound
		}
		if err != nil {
			return View{}, err
		}
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return View{}, err
		}
		if !visibleTo(ctx, t, tk, subject, groups) {
			return View{}, dms.ErrNotFound
		}
		return view(ctx, t, tk, subject, groups)
	})
}

// viewOf shows a task to subject, given the entries that answered it.
func viewOf(tk taskstore.Task, subject string, groups []string, answered []string) View {
	v := toView(tk)
	for _, e := range entriesOf(tk, subject, groups) {
		if slices.Contains(answered, e) {
			v.Answered = true
		} else if tk.Status == taskstore.StatusOpen {
			v.CanRespond = true
		}
	}
	return v
}

// view shows one task to subject with its responses.
func view(ctx context.Context, t *dms.Service, tk taskstore.Task, subject string, groups []string) (View, error) {
	rows, err := t.DAL.Tasks().Responses(ctx, []string{tk.ID}, nil)
	if err != nil {
		return View{}, err
	}
	answered := make([]string, len(rows))
	for i, r := range rows {
		answered[i] = r.Assignee
	}
	v := viewOf(tk, subject, groups, answered)
	v.CanReassign = tk.Status == taskstore.StatusOpen && (tk.CreatedBy == subject && tk.Source != taskstore.SourceWorkflow || manages(ctx, t, subject, tk.WorkspaceID))
	v.Responses = make([]ResponseView, len(rows))
	for i, r := range rows {
		v.Responses[i] = ResponseView{Subject: r.Subject, Assignee: r.Assignee, Outcome: r.Outcome, Comment: r.Comment, Input: r.Input, CreatedAt: r.CreatedAt}
	}
	return v, nil
}

// Create creates a standalone task as subject.
func (s *Service) Create(ctx context.Context, subject string, in Input) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		in.Workflow, in.System, in.Wait = nil, false, false
		tk, err := Create(ctx, t, subject, in)
		if err != nil {
			return err
		}
		out, err = view(ctx, t, tk, subject, nil)
		return err
	})
	return out, err
}

// Respond records subject's answer.
func (s *Service) Respond(ctx context.Context, subject, id string, r Response) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		tk, err := Respond(ctx, t, subject, id, r)
		if err != nil {
			return err
		}
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return err
		}
		out, err = view(ctx, t, tk, subject, groups)
		return err
	})
	return out, err
}

// Reassign replaces an open task's assignees.
func (s *Service) Reassign(ctx context.Context, subject, id string, assignees []string) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		tk, err := Reassign(ctx, t, subject, id, assignees)
		if err != nil {
			return err
		}
		out, err = view(ctx, t, tk, subject, nil)
		return err
	})
	return out, err
}

// Cancel cancels a person's or the system's open task.
func (s *Service) Cancel(ctx context.Context, subject, id string) (View, error) {
	var out View
	err := s.DMS.Write(ctx, func(t *dms.Service) error {
		tk, err := Cancel(ctx, t, subject, id)
		if err != nil {
			return err
		}
		out, err = view(ctx, t, tk, subject, nil)
		return err
	})
	return out, err
}

// Result reads a task a run waits for: the node's output once the task is
// completed, or nil while it is open. A cancelled task is ErrCancelled.
func Result(ctx context.Context, t *dms.Service, id string) (map[string]any, error) {
	tk, err := t.DAL.Tasks().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	switch tk.Status {
	case taskstore.StatusOpen:
		return nil, nil
	case taskstore.StatusCancelled:
		return nil, ErrCancelled
	}
	out := map[string]any{"task_id": tk.ID, "outcome": tk.Outcome, "comment": tk.Comment, "status": string(tk.Status)}
	if tk.CompletedBy != "" {
		out["completed_by"] = tk.CompletedBy
	}
	if len(tk.Input) > 0 {
		var input any
		if json.Unmarshal(tk.Input, &input) == nil {
			out["input"] = input
		}
	}
	rows, err := t.DAL.Tasks().Responses(ctx, []string{tk.ID}, nil)
	if err != nil {
		return nil, err
	}
	responses := make([]any, len(rows))
	for i, r := range rows {
		resp := map[string]any{"subject": r.Subject, "assignee": r.Assignee, "outcome": r.Outcome, "comment": r.Comment}
		if len(r.Input) > 0 {
			var input any
			if json.Unmarshal(r.Input, &input) == nil {
				resp["input"] = input
			}
		}
		responses[i] = resp
	}
	out["responses"] = responses
	return out, nil
}
