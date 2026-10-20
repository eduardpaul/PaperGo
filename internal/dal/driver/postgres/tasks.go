package postgres

import (
	"context"
	"strings"

	"github.com/aarondl/opt/omit"
	"github.com/stephenafamo/bob/dialect/psql"
	"github.com/stephenafamo/bob/dialect/psql/sm"
	"github.com/stephenafamo/bob/types"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/driver/nullable"
	"papergo/internal/dal/driver/postgres/models"
	"papergo/internal/dal/tasks"
)

// LockTask locks the task's workspace, then reads the task under a row lock so
// status, version and responses reflect the latest committed change.
func (q Queries) LockTask(ctx context.Context, id string) (tasks.Task, error) {
	task, err := models.FindTask(ctx, q.exec, id, "workspace_id")
	if err != nil {
		return tasks.Task{}, dberr.Translate(err)
	}
	if err := q.LockWorkspace(ctx, task.WorkspaceID); err != nil {
		return tasks.Task{}, err
	}
	row, err := models.Tasks.Query(models.SelectWhere.Tasks.ID.EQ(id), sm.ForUpdate()).One(ctx, q.exec)
	if err != nil {
		return tasks.Task{}, dberr.Translate(err)
	}
	return taskRecord(row), nil
}

func (q Queries) Task(ctx context.Context, id string) (tasks.Task, error) {
	row, err := models.FindTask(ctx, q.exec, id)
	if err != nil {
		return tasks.Task{}, dberr.Translate(err)
	}
	return taskRecord(row), nil
}

func (q Queries) Tasks(ctx context.Context, f tasks.Filter) ([]tasks.Task, error) {
	query := models.Tasks.Query()
	w := models.SelectWhere.Tasks
	if f.Waiting {
		f.Status = tasks.StatusOpen
	}
	if f.Waiting || f.AssignedOnly {
		query.Apply(sm.Where(assignedTo(f)))
	}
	if f.Status != "" {
		query.Apply(w.Status.EQ(string(f.Status)))
	}
	if f.Kind != "" {
		query.Apply(w.Kind.EQ(string(f.Kind)))
	}
	if f.Key != "" {
		query.Apply(w.Key.EQ(f.Key))
	}
	if f.WorkspaceID != "" {
		query.Apply(w.WorkspaceID.EQ(f.WorkspaceID))
	}
	if f.ItemID != "" {
		query.Apply(w.ItemID.EQ(f.ItemID))
	}
	if f.RunID != "" {
		query.Apply(w.RunID.EQ(f.RunID))
	}
	if f.WaitOnly {
		query.Apply(w.Wait.EQ(true))
	}
	c := models.Tasks.Columns
	if f.Status == tasks.StatusOpen {
		query.Apply(sm.OrderBy(c.DueAt).Asc().NullsLast(), sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	} else {
		query.Apply(sm.OrderBy(c.CreatedAt).Desc(), sm.OrderBy(c.ID).Desc())
	}
	if f.Limit > 0 {
		query.Apply(sm.Limit(f.Limit))
	}
	if f.Offset > 0 {
		query.Apply(sm.Offset(f.Offset))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]tasks.Task, len(rows))
	for i, row := range rows {
		out[i] = taskRecord(row)
	}
	return out, nil
}

// assignedTo matches tasks assigned to the filter's subject or groups and,
// when waiting, keeps entries that have not answered.
func assignedTo(f tasks.Filter) psql.Expression {
	entries := []any{f.Subject}
	for _, group := range f.Groups {
		entries = append(entries, "group:"+group)
	}
	query := `EXISTS (SELECT 1 FROM jsonb_array_elements_text("tasks"."assignees") AS j(value) WHERE j.value IN (` + marks(len(entries)) + `)`
	if f.Waiting {
		query += ` AND NOT EXISTS (SELECT 1 FROM task_responses r WHERE r.task_id = "tasks"."id" AND r.assignee = j.value)`
	}
	return psql.Raw(query+`)`, entries...)
}

func (q Queries) InsertTask(ctx context.Context, t tasks.Task) error {
	_, err := models.Tasks.Insert(&models.TaskSetter{
		ID: omit.From(t.ID), CreatedAt: omit.From(t.CreatedAt), UpdatedAt: omit.From(t.UpdatedAt), Version: omit.From(t.Version),
		WorkspaceID: omit.From(t.WorkspaceID), ItemID: nullable.String(t.ItemID), Kind: omit.From(string(t.Kind)), Key: omit.From(t.Key),
		Title: omit.From(t.Title), Description: omit.From(t.Description), Assignees: omit.From(types.NewJSON(t.Assignees)),
		Completion: omit.From(string(t.Completion)), Outcomes: omit.From(types.NewJSON(t.Outcomes)),
		InputSchema: nullable.Object(t.InputSchema), Values: nullable.Object(t.Values), Review: nullable.Object(t.Review),
		Priority: omit.From(string(t.Priority)), DueAt: nullable.Time(t.DueAt), Source: omit.From(string(t.Source)),
		WorkflowID: nullable.String(t.WorkflowID), RunID: nullable.String(t.RunID), Node: nullable.String(t.Node), Wait: omit.From(t.Wait),
		Status: omit.From(string(t.Status)), Outcome: nullable.String(t.Outcome), CompletedBy: nullable.String(t.CompletedBy),
		CompletedAt: nullable.Time(t.CompletedAt), Comment: omit.From(t.Comment), Input: nullable.JSON(t.Input), CreatedBy: omit.From(t.CreatedBy),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) UpdateTask(ctx context.Context, t tasks.Task, from int64) error {
	set := &models.TaskSetter{
		UpdatedAt: omit.From(t.UpdatedAt), Version: omit.From(t.Version), Assignees: omit.From(types.NewJSON(t.Assignees)),
		Status: omit.From(string(t.Status)), Outcome: nullable.String(t.Outcome), CompletedBy: nullable.String(t.CompletedBy),
		CompletedAt: nullable.Time(t.CompletedAt), Comment: omit.From(t.Comment), Input: nullable.JSON(t.Input),
	}
	w := models.UpdateWhere.Tasks
	n, err := models.Tasks.Update(set.UpdateMod(), w.ID.EQ(t.ID), w.Version.EQ(from)).Exec(ctx, q.exec)
	if err != nil {
		return dberr.Translate(err)
	}
	if n != 1 {
		return dberr.ErrConcurrentUpdate
	}
	return nil
}

func (q Queries) InsertResponse(ctx context.Context, r tasks.Response) error {
	_, err := models.TaskResponses.Insert(&models.TaskResponseSetter{
		ID: omit.From(r.ID), CreatedAt: omit.From(r.CreatedAt), TaskID: omit.From(r.TaskID), Subject: omit.From(r.Subject),
		Assignee: omit.From(r.Assignee), Outcome: omit.From(r.Outcome), Comment: omit.From(r.Comment), Input: nullable.JSON(r.Input),
	}).Exec(ctx, q.exec)
	return dberr.Translate(err)
}

func (q Queries) Responses(ctx context.Context, taskIDs, assignees []string) ([]tasks.Response, error) {
	w := models.SelectWhere.TaskResponses
	c := models.TaskResponses.Columns
	query := models.TaskResponses.Query(w.TaskID.In(taskIDs...), sm.OrderBy(c.CreatedAt), sm.OrderBy(c.ID))
	if len(assignees) > 0 {
		query.Apply(w.Assignee.In(assignees...))
	}
	rows, err := query.All(ctx, q.exec)
	if err != nil {
		return nil, dberr.Translate(err)
	}
	out := make([]tasks.Response, len(rows))
	for i, r := range rows {
		out[i] = tasks.Response{ID: r.ID, TaskID: r.TaskID, Subject: r.Subject, Assignee: r.Assignee, Outcome: r.Outcome,
			Comment: r.Comment, Input: r.Input.GetOrZero().Val, CreatedAt: r.CreatedAt.UTC()}
	}
	return out, nil
}

func taskRecord(r *models.Task) tasks.Task {
	return tasks.Task{
		ID: r.ID, WorkspaceID: r.WorkspaceID, ItemID: r.ItemID.GetOrZero(), Kind: tasks.Kind(r.Kind), Key: r.Key, Title: r.Title, Description: r.Description,
		Assignees: r.Assignees.Val, Completion: tasks.Completion(r.Completion), Outcomes: r.Outcomes.Val,
		InputSchema: r.InputSchema.GetOrZero().Val, Values: r.Values.GetOrZero().Val, Review: r.Review.GetOrZero().Val,
		Priority: tasks.Priority(r.Priority), DueAt: nullable.UTC(r.DueAt), Source: tasks.Source(r.Source),
		WorkflowID: r.WorkflowID.GetOrZero(), RunID: r.RunID.GetOrZero(), Node: r.Node.GetOrZero(), Wait: r.Wait,
		Status: tasks.Status(r.Status), Outcome: r.Outcome.GetOrZero(), CompletedBy: r.CompletedBy.GetOrZero(), CompletedAt: nullable.UTC(r.CompletedAt),
		Comment: r.Comment, Input: r.Input.GetOrZero().Val, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(), Version: r.Version,
	}
}

// marks returns n comma-separated ? placeholders.
func marks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
