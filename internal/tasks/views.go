package tasks

import (
	"context"
	"encoding/json"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"papergo/ent"
	"papergo/ent/task"
	"papergo/ent/taskresponse"
	"papergo/internal/dms"
	"papergo/internal/model"
)

// View is a task as the API shows it.
type View struct {
	ID          string              `json:"id"`
	WorkspaceID string              `json:"workspace_id"`
	ItemID      *string             `json:"item_id,omitempty"`
	Kind        string              `json:"kind"`
	Key         string              `json:"key,omitempty"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Assignees   []string            `json:"assignees"`
	Completion  string              `json:"completion"`
	Outcomes    []model.TaskOutcome `json:"outcomes"`
	InputSchema map[string]any      `json:"input_schema,omitempty"`
	Values      map[string]any      `json:"values,omitempty"`
	Review      map[string]any      `json:"review,omitempty"`
	Priority    string              `json:"priority"`
	DueAt       *time.Time          `json:"due_at,omitempty"`
	Source      string              `json:"source"`
	WorkflowID  *string             `json:"workflow_id,omitempty"`
	RunID       *string             `json:"run_id,omitempty"`
	Node        *string             `json:"node,omitempty"`
	Status      string              `json:"status"`
	Outcome     *string             `json:"outcome,omitempty"`
	CompletedBy *string             `json:"completed_by,omitempty"`
	CompletedAt *time.Time          `json:"completed_at,omitempty"`
	Comment     string              `json:"comment,omitempty"`
	Input       json.RawMessage     `json:"input,omitempty"`
	CreatedBy   string              `json:"created_by"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Version     int                 `json:"version"`
	// Responses are the answers so far, oldest first; only in single reads.
	Responses []ResponseView `json:"responses,omitempty"`
	// Answered tells the reader that they, or their group, already answered.
	Answered bool `json:"answered,omitempty"`
	// CanRespond tells the reader whether they may answer now.
	CanRespond bool `json:"can_respond"`
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

func toView(tk *ent.Task) View {
	return View{ID: tk.ID, WorkspaceID: tk.WorkspaceID, ItemID: tk.ItemID, Kind: string(tk.Kind), Key: tk.Key, Title: tk.Title, Description: tk.Description,
		Assignees: tk.Assignees, Completion: string(tk.Completion), Outcomes: tk.Outcomes, InputSchema: tk.InputSchema, Values: tk.Values, Review: tk.Review,
		Priority: string(tk.Priority), DueAt: tk.DueAt, Source: string(tk.Source), WorkflowID: tk.WorkflowID, RunID: tk.RunID, Node: tk.Node,
		Status: string(tk.Status), Outcome: tk.Outcome, CompletedBy: tk.CompletedBy, CompletedAt: tk.CompletedAt, Comment: tk.Comment, Input: tk.Input,
		CreatedBy: tk.CreatedBy, CreatedAt: tk.CreatedAt, UpdatedAt: tk.UpdatedAt, Version: tk.Version}
}

// Filter selects tasks.
type Filter struct {
	// Status is open, completed, cancelled or expired; empty is all.
	Status      string
	Kind        string
	Key         string
	WorkspaceID string
	ItemID      string
	// Waiting keeps the open tasks the reader still has to answer.
	Waiting bool
	Limit   int
}

func (f Filter) apply(q *ent.TaskQuery) (*ent.TaskQuery, error) {
	if f.Waiting {
		f.Status = string(task.StatusOpen)
	}
	if f.Status != "" {
		if task.StatusValidator(task.Status(f.Status)) != nil {
			return nil, dms.Invalid("status is open, completed, cancelled or expired")
		}
		q.Where(task.StatusEQ(task.Status(f.Status)))
	}
	if f.Kind != "" {
		if task.KindValidator(task.Kind(f.Kind)) != nil {
			return nil, dms.Invalid("kind is task or approval")
		}
		q.Where(task.KindEQ(task.Kind(f.Kind)))
	}
	if f.Key != "" {
		q.Where(task.KeyEQ(f.Key))
	}
	if f.WorkspaceID != "" {
		q.Where(task.WorkspaceIDEQ(f.WorkspaceID))
	}
	if f.ItemID != "" {
		q.Where(task.ItemIDEQ(f.ItemID))
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if f.Status == string(task.StatusOpen) {
		// Open tasks by due time, those without one last, then oldest first.
		return q.Order(task.ByDueAt(entsql.OrderNullsLast()), task.ByCreatedAt(), task.ByID()).Limit(limit), nil
	}
	return q.Order(ent.Desc(task.FieldCreatedAt), ent.Desc(task.FieldID)).Limit(limit), nil
}

// Service reads and writes tasks for the API, each call in its own
// transaction.
type Service struct {
	DMS *dms.Service
}

// Mine lists the tasks assigned to subject or their groups.
func (s *Service) Mine(ctx context.Context, subject string, f Filter) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		groups, err := t.GroupIDs(ctx, subject)
		if err != nil {
			return nil, err
		}
		q, err := f.apply(t.Client.Task.Query().Where(assignedTo(subject, groups)))
		if err != nil {
			return nil, err
		}
		rows, err := q.All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]View, 0, len(rows))
		for _, tk := range rows {
			// Lost access hides a task without changing it.
			if CanReview(ctx, t, subject, tk.WorkspaceID, deref(tk.ItemID)) != nil {
				continue
			}
			v, err := view(ctx, t, tk, subject, groups, false)
			if err != nil {
				return nil, err
			}
			if f.Waiting && !v.CanRespond {
				continue
			}
			out = append(out, v)
		}
		return out, nil
	})
}

// Workspace lists a workspace's tasks to its owners.
func (s *Service) Workspace(ctx context.Context, subject, workspaceID string, f Filter) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		if !manages(ctx, t, subject, workspaceID) {
			if _, err := t.Authorize(ctx, subject, workspaceID, "read"); err != nil {
				return nil, err
			}
			return nil, dms.ErrForbidden
		}
		f.WorkspaceID = workspaceID
		return list(ctx, t, subject, f, false)
	})
}

// Item lists the tasks about an item that subject may see.
func (s *Service) Item(ctx context.Context, subject, itemID string, f Filter) ([]View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) ([]View, error) {
		if _, err := t.Authorize(ctx, subject, itemID, "read_draft"); err != nil {
			return nil, err
		}
		f.ItemID = itemID
		return list(ctx, t, subject, f, true)
	})
}

func list(ctx context.Context, t *dms.Service, subject string, f Filter, check bool) ([]View, error) {
	groups, err := t.GroupIDs(ctx, subject)
	if err != nil {
		return nil, err
	}
	q, err := f.apply(t.Client.Task.Query())
	if err != nil {
		return nil, err
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(rows))
	for _, tk := range rows {
		if check && !visibleTo(ctx, t, tk, subject, groups) {
			continue
		}
		v, err := view(ctx, t, tk, subject, groups, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Get returns a task with its responses to whoever may see it.
func (s *Service) Get(ctx context.Context, subject, id string) (View, error) {
	return dms.Read(ctx, s.DMS, func(t *dms.Service) (View, error) {
		tk, err := t.Client.Task.Get(ctx, id)
		if ent.IsNotFound(err) {
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
		return view(ctx, t, tk, subject, groups, true)
	})
}

func view(ctx context.Context, t *dms.Service, tk *ent.Task, subject string, groups []string, responses bool) (View, error) {
	v := toView(tk)
	entry, assigned := entryOf(tk, subject, groups)
	if assigned {
		answered, err := t.Client.TaskResponse.Query().Where(taskresponse.TaskIDEQ(tk.ID), taskresponse.AssigneeEQ(entry)).Exist(ctx)
		if err != nil {
			return View{}, err
		}
		v.Answered = answered
		v.CanRespond = tk.Status == task.StatusOpen && !answered
	}
	if responses {
		rows, err := t.Client.TaskResponse.Query().Where(taskresponse.TaskIDEQ(tk.ID)).Order(ent.Asc(taskresponse.FieldCreatedAt), ent.Asc(taskresponse.FieldID)).All(ctx)
		if err != nil {
			return View{}, err
		}
		v.Responses = make([]ResponseView, len(rows))
		for i, r := range rows {
			v.Responses[i] = ResponseView{Subject: r.Subject, Assignee: r.Assignee, Outcome: r.Outcome, Comment: r.Comment, Input: r.Input, CreatedAt: r.CreatedAt}
		}
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
		out, err = view(ctx, t, tk, subject, nil, true)
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
		out, err = view(ctx, t, tk, subject, groups, true)
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
		out, err = view(ctx, t, tk, subject, nil, true)
		return err
	})
	return out, err
}

// Result reads a task a run waits for: the node's output once the task is
// completed, or nil while it is open. A cancelled task is ErrCancelled.
func Result(ctx context.Context, t *dms.Service, id string) (map[string]any, error) {
	tk, err := t.Client.Task.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	switch tk.Status {
	case task.StatusOpen:
		return nil, nil
	case task.StatusCancelled:
		return nil, ErrCancelled
	}
	out := map[string]any{"task_id": tk.ID, "outcome": deref(tk.Outcome), "comment": tk.Comment, "status": string(tk.Status)}
	if tk.CompletedBy != nil {
		out["completed_by"] = *tk.CompletedBy
	}
	if len(tk.Input) > 0 {
		var input any
		if json.Unmarshal(tk.Input, &input) == nil {
			out["input"] = input
		}
	}
	rows, err := t.Client.TaskResponse.Query().Where(taskresponse.TaskIDEQ(tk.ID)).Order(ent.Asc(taskresponse.FieldCreatedAt), ent.Asc(taskresponse.FieldID)).All(ctx)
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
