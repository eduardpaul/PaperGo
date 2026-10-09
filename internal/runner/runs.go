package runner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/google/uuid"

	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/ent/workflowrun"
	"papergo/ent/workflowrunitem"
	"papergo/internal/dms"
)

// Run is a workflow run as the API shows it.
type Run struct {
	ID              string  `json:"id"`
	WorkflowID      string  `json:"workflow_id"`
	WorkflowVersion int     `json:"workflow_version"`
	WorkspaceID     string  `json:"workspace_id"`
	ItemID          *string `json:"item_id,omitempty"`
	// Items is a selection run's ordered membership; ItemID is its primary.
	Items     []string   `json:"items,omitempty"`
	EventID   string     `json:"event_id"`
	EventType string     `json:"event_type"`
	Depth     int        `json:"depth"`
	Actor     string     `json:"actor"`
	RetryOf   *string    `json:"retry_of,omitempty"`
	Status    string     `json:"status"`
	Error     string     `json:"error,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	// Inputs are the launch form's values of a manual run, defaults applied.
	Inputs map[string]any `json:"inputs,omitempty"`
	Result any            `json:"result,omitempty"`
	Steps  []Step         `json:"steps,omitempty"`
}

// Step is one recorded step of a run: the load, each node attempt, and the
// finish.
type Step struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Output      any        `json:"output,omitempty"`
	Error       string     `json:"error,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// RunFilter selects runs of a workspace.
type RunFilter struct {
	WorkflowID string
	ItemID     string
	After      string
	Limit      int
}

// RunPage is one page of runs, newest first.
type RunPage struct {
	Data       []Run  `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func status(s dbos.WorkflowStatusType) string {
	switch s {
	case dbos.WorkflowStatusEnqueued, dbos.WorkflowStatusDelayed:
		return "queued"
	case dbos.WorkflowStatusPending:
		return "running"
	case dbos.WorkflowStatusSuccess:
		return "completed"
	case dbos.WorkflowStatusCancelled:
		return "cancelled"
	}
	return "failed"
}

func toRun(row *ent.WorkflowRun) Run {
	return Run{ID: row.ID, WorkflowID: row.WorkflowID, WorkflowVersion: row.WorkflowVersion, WorkspaceID: row.WorkspaceID, ItemID: row.ItemID, EventID: row.EventID, EventType: row.EventType,
		Depth: row.Depth, Actor: row.Actor, RetryOf: row.RetryOf, Status: "queued", CreatedAt: row.CreatedAt}
}

func (r *Runner) attach(runs []Run, output bool) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.ID
	}
	// Errors are stored with the output, so it is always loaded.
	statuses, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterWorkflowIDs(ids...), dbos.WithFilterLoadInput(output), dbos.WithFilterLoadOutput(true))
	if err != nil {
		return err
	}
	byID := map[string]dbos.WorkflowStatus{}
	for _, s := range statuses {
		byID[s.ID] = s
	}
	for i := range runs {
		s, ok := byID[runs[i].ID]
		if !ok {
			continue
		}
		runs[i].Status = status(s.Status)
		if !s.UpdatedAt.IsZero() {
			t := s.UpdatedAt.UTC()
			runs[i].UpdatedAt = &t
		}
		if s.Error != nil {
			runs[i].Error = s.Error.Error()
		}
		if output {
			runs[i].Result = decodeOutput(s.Output)
			if in, ok := decodeOutput(s.Input).(map[string]any); ok {
				runs[i].Inputs, _ = in["inputs"].(map[string]any)
			}
		}
	}
	return nil
}

func manage(ctx context.Context, t *dms.Service, subject, workspaceID string) error {
	_, err := t.Authorize(ctx, subject, workspaceID, "manage")
	return err
}

// Runs lists a workspace's runs, newest first, to its managers.
func (r *Runner) Runs(ctx context.Context, subject, workspaceID string, f RunFilter) (RunPage, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	page, err := dms.Read(ctx, r.dms, func(t *dms.Service) (RunPage, error) {
		if err := manage(ctx, t, subject, workspaceID); err != nil {
			return RunPage{}, err
		}
		q := t.Client.WorkflowRun.Query().Where(workflowrun.WorkspaceIDEQ(workspaceID))
		if f.WorkflowID != "" {
			q.Where(workflowrun.WorkflowIDEQ(f.WorkflowID))
		}
		if f.ItemID != "" {
			// Selection runs count for every member, not only their primary item.
			q.Where(workflowrun.Or(workflowrun.ItemIDEQ(f.ItemID), func(s *entsql.Selector) {
				s.Where(entsql.In(s.C(workflowrun.FieldID), entsql.Select(workflowrunitem.FieldRunID).From(entsql.Table(workflowrunitem.Table)).Where(entsql.EQ(workflowrunitem.FieldItemID, f.ItemID))))
			}))
		}
		if f.After != "" {
			at, id, err := decodeCursor(f.After)
			if err != nil {
				return RunPage{}, err
			}
			q.Where(func(s *entsql.Selector) {
				s.Where(entsql.Or(entsql.LT(s.C(workflowrun.FieldCreatedAt), at), entsql.And(entsql.EQ(s.C(workflowrun.FieldCreatedAt), at), entsql.LT(s.C(workflowrun.FieldID), id))))
			})
		}
		rows, err := q.Order(ent.Desc(workflowrun.FieldCreatedAt), ent.Desc(workflowrun.FieldID)).Limit(f.Limit + 1).All(ctx)
		if err != nil {
			return RunPage{}, err
		}
		out := RunPage{Data: make([]Run, 0, len(rows))}
		if len(rows) > f.Limit {
			last := rows[f.Limit-1]
			out.NextCursor = encodeCursor(last.CreatedAt, last.ID)
			rows = rows[:f.Limit]
		}
		for _, row := range rows {
			out.Data = append(out.Data, toRun(row))
		}
		return out, members(ctx, t, out.Data)
	})
	if err != nil {
		return page, err
	}
	return page, r.attach(page.Data, false)
}

// members fills in the membership of selection runs.
func members(ctx context.Context, t *dms.Service, runs []Run) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]string, len(runs))
	at := map[string]int{}
	for i, run := range runs {
		ids[i], at[run.ID] = run.ID, i
	}
	rows, err := t.Client.WorkflowRunItem.Query().Where(workflowrunitem.RunIDIn(ids...)).Order(ent.Asc(workflowrunitem.FieldRunID), ent.Asc(workflowrunitem.FieldPosition)).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		i := at[row.RunID]
		runs[i].Items = append(runs[i].Items, row.ItemID)
	}
	return nil
}

func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return time.Time{}, "", dms.Invalid("invalid cursor")
	}
	return t, id, nil
}

func (r *Runner) row(ctx context.Context, subject, id string) (*ent.WorkflowRun, error) {
	return dms.Read(ctx, r.dms, func(t *dms.Service) (*ent.WorkflowRun, error) {
		row, err := t.Client.WorkflowRun.Get(ctx, id)
		if ent.IsNotFound(err) {
			return nil, dms.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		return row, manage(ctx, t, subject, row.WorkspaceID)
	})
}

// pending returns a manual run whose event is logged but not dispatched yet:
// the ID that StartRuns returned exists before the dispatcher records it.
func (r *Runner) pending(ctx context.Context, subject, id string) (Run, error) {
	return dms.Read(ctx, r.dms, func(t *dms.Service) (Run, error) {
		rest, ok := strings.CutPrefix(id, "wf:")
		workflowID, eventID, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 {
			return Run{}, dms.ErrNotFound
		}
		e, err := t.Client.DomainEvent.Get(ctx, eventID)
		if ent.IsNotFound(err) || err == nil && (e.DispatchedAt != nil || e.Data["workflow_id"] != workflowID) {
			return Run{}, dms.ErrNotFound
		}
		if err != nil {
			return Run{}, err
		}
		if err = manage(ctx, t, subject, e.WorkspaceID); err != nil {
			return Run{}, err
		}
		return Run{ID: id, WorkflowID: workflowID, WorkspaceID: e.WorkspaceID, ItemID: e.ResourceID, EventID: e.ID, EventType: e.Type, Depth: e.Depth, Actor: e.Actor, Status: "queued", CreatedAt: e.CreatedAt}, nil
	})
}

// Run returns one run with its result and steps to workspace managers.
func (r *Runner) Run(ctx context.Context, subject, id string) (Run, error) {
	row, err := r.row(ctx, subject, id)
	if errors.Is(err, dms.ErrNotFound) {
		return r.pending(ctx, subject, id)
	}
	if err != nil {
		return Run{}, err
	}
	runs := []Run{toRun(row)}
	if _, err = dms.Read(ctx, r.dms, func(t *dms.Service) (struct{}, error) { return struct{}{}, members(ctx, t, runs) }); err != nil {
		return Run{}, err
	}
	if err = r.attach(runs, true); err != nil {
		return Run{}, err
	}
	steps, err := dbos.GetWorkflowSteps(r.ctx, id)
	if err != nil {
		return Run{}, err
	}
	runs[0].Steps = make([]Step, 0, len(steps))
	for _, s := range steps {
		step := Step{ID: s.StepID, Name: s.StepName, Output: decodeOutput(s.Output)}
		if s.Error != nil {
			step.Error = s.Error.Error()
		}
		if !s.StartedAt.IsZero() {
			t := s.StartedAt.UTC()
			step.StartedAt = &t
		}
		if !s.CompletedAt.IsZero() {
			t := s.CompletedAt.UTC()
			step.CompletedAt = &t
		}
		runs[0].Steps = append(runs[0].Steps, step)
	}
	return runs[0], nil
}

// decodeOutput shows a step's recorded JSON as JSON, not as a string.
func decodeOutput(v any) any {
	var raw []byte
	switch t := v.(type) {
	case string:
		raw = []byte(t)
	case []byte:
		raw = t
	case json.RawMessage:
		raw = t
	default:
		return v
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return v
	}
	return out
}

// Cancel stops a queued or running run.
func (r *Runner) Cancel(ctx context.Context, subject, id string) (Run, error) {
	if _, err := r.row(ctx, subject, id); err != nil {
		return Run{}, err
	}
	if err := dbos.CancelWorkflow(r.ctx, id); err != nil {
		return Run{}, err
	}
	return r.Run(ctx, subject, id)
}

// Retry runs a failed run again from the step that failed, as a new run:
// the steps before it keep their recorded results, so nothing they wrote is
// written twice.
func (r *Runner) Retry(ctx context.Context, subject, id string) (Run, error) {
	row, err := r.row(ctx, subject, id)
	if err != nil {
		return Run{}, err
	}
	current := []Run{toRun(row)}
	if err = r.attach(current, false); err != nil {
		return Run{}, err
	}
	if current[0].Status != "failed" {
		return Run{}, dms.Invalid("only failed runs can be retried")
	}
	steps, err := dbos.GetWorkflowSteps(r.ctx, id, dbos.WithStepsLoadOutput(false))
	if err != nil {
		return Run{}, err
	}
	start := -1
	for _, s := range steps {
		if s.Error != nil {
			start = s.StepID
		}
	}
	if start < 0 {
		return Run{}, errors.New("the failed run has no failed step")
	}
	retryID := "retry:" + uuid.NewString()
	// DBOS writes the fork on its own connection, so it must not run inside a
	// PaperGo write transaction, which holds the SQLite write lock.
	if _, err = dbos.ForkWorkflow[any](r.ctx, dbos.ForkWorkflowInput{OriginalWorkflowID: id, ForkedWorkflowID: retryID, StartStep: uint(start), QueueName: runQueue}); err != nil {
		return Run{}, fmt.Errorf("retry run %s: %w", id, err)
	}
	err = r.dms.Write(ctx, func(t *dms.Service) error {
		c := t.Client.WorkflowRun.Create().SetID(retryID).SetWorkflowID(row.WorkflowID).SetWorkflowVersion(row.WorkflowVersion).SetWorkspaceID(row.WorkspaceID).
			SetEventID(retryID).SetEventType(row.EventType).SetDepth(row.Depth).SetActor(subject).SetRetryOf(row.ID)
		if row.ItemID != nil {
			c.SetItemID(*row.ItemID)
		}
		if _, err := c.Save(ctx); err != nil {
			return err
		}
		// A retried selection keeps its members, in order.
		original, err := t.Client.WorkflowRunItem.Query().Where(workflowrunitem.RunIDEQ(row.ID)).All(ctx)
		if err != nil {
			return err
		}
		for _, m := range original {
			if err = t.Client.WorkflowRunItem.Create().SetID(fmt.Sprintf("%s:%d", retryID, m.Position)).SetRunID(retryID).SetItemID(m.ItemID).SetPosition(m.Position).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		_ = dbos.CancelWorkflow(r.ctx, retryID)
		return Run{}, fmt.Errorf("retry run %s: %w", id, err)
	}
	return r.Run(ctx, subject, retryID)
}

// purge deletes runs that finished more than the retention ago, with their
// step markers first: a crash in between leaves only finished runs without
// markers, which never replay, and markers never outlive their run. Events
// dispatched before the retention go too.
func (r *Runner) purge(ctx dbos.Context, _ dbos.ScheduledWorkflowInput) (any, error) {
	return r.Purge(ctx)
}

// Purge applies the retention now.
func (r *Runner) Purge(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-r.cfg.Retention)
	if err := r.dms.Write(ctx, func(t *dms.Service) error {
		_, err := t.Client.DomainEvent.Delete().Where(domainevent.DispatchedAtLT(cutoff)).Exec(ctx)
		return err
	}); err != nil {
		return 0, err
	}
	deleted := 0
	for {
		done, err := dbos.ListWorkflows(r.ctx, dbos.WithFilterName(runName), dbos.WithFilterStatus(dbos.WorkflowStatusSuccess, dbos.WorkflowStatusError, dbos.WorkflowStatusCancelled, dbos.WorkflowStatusMaxRecoveryAttemptsExceeded),
			dbos.WithFilterCompletedBefore(cutoff), dbos.WithFilterLimit(200), dbos.WithFilterLoadInput(false), dbos.WithFilterLoadOutput(false))
		if err != nil || len(done) == 0 {
			return deleted, err
		}
		ids := make([]string, len(done))
		for i, s := range done {
			ids[i] = s.ID
		}
		if err = r.dms.Write(ctx, func(t *dms.Service) error {
			args := make([]any, len(ids))
			marks := make([]string, len(ids))
			for i, id := range ids {
				args[i], marks[i] = id, "?"
			}
			if _, err := t.Client.ExecContext(ctx, `DELETE FROM runner_step_results WHERE run_id IN (`+strings.Join(marks, ",")+`)`, args...); err != nil {
				return err
			}
			_, err := t.Client.WorkflowRun.Delete().Where(workflowrun.IDIn(ids...)).Exec(ctx)
			return err
		}); err != nil {
			return deleted, err
		}
		if err = dbos.DeleteWorkflows(r.ctx, ids); err != nil {
			return deleted, err
		}
		deleted += len(ids)
	}
}
