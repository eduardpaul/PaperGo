package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"papergo/internal/model"
)

// Task is an action request: its assignees answer with one of its outcomes,
// optionally with a form and a review of a staged file. Workflows, people
// and PaperGo itself create tasks; an approval is a task of kind approval.
// A workflow run that waits for a task is parked: it holds no worker, and
// the task's completion resumes it.
type Task struct{ ent.Schema }

func (Task) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable(),
		field.String("item_id").Optional().Nillable().Immutable(),
		field.Enum("kind").Values("task", "approval").Default("task").Immutable(),
		field.String("key").Default("").MaxLen(64).Immutable(),
		field.String("title").NotEmpty().MaxLen(500),
		field.String("description").Default("").MaxLen(4000),
		// Assignees are subjects and group:<id> entries.
		field.JSON("assignees", []string{}),
		field.Enum("completion").Values("any", "all").Default("any").Immutable(),
		field.JSON("outcomes", []model.TaskOutcome{}).Immutable(),
		// InputSchema is the form answers fill in, frozen when the task is
		// created; Values prefill it.
		field.JSON("input_schema", map[string]any{}).Optional().Immutable(),
		field.JSON("values", map[string]any{}).Optional().Immutable(),
		field.JSON("review", map[string]any{}).Optional().Immutable(),
		field.Enum("priority").Values("low", "normal", "high").Default("normal"),
		field.Time("due_at").Optional().Nillable(),
		field.Enum("source").Values("workflow", "user", "system").Immutable(),
		field.String("workflow_id").Optional().Nillable().Immutable(),
		field.String("run_id").Optional().Nillable().Immutable(),
		field.String("node").Optional().Nillable().Immutable(),
		// Wait marks a task whose result a workflow consumes.
		field.Bool("wait").Default(false).Immutable(),
		field.Enum("status").Values("open", "completed", "cancelled", "expired").Default("open"),
		field.String("outcome").Optional().Nillable(),
		field.String("completed_by").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.String("comment").Default("").MaxLen(4000),
		field.JSON("input", json.RawMessage{}).Optional(),
		field.String("created_by").Immutable(),
		field.Time("updated_at").Default(func() time.Time { return time.Now().UTC() }).UpdateDefault(func() time.Time { return time.Now().UTC() }),
		field.Int("version").Positive().Default(1),
	}
}
func (Task) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id"), index.Fields("workspace_id", "status"), index.Fields("item_id")}
}

// TaskResponse is one assignee's answer to a task. An any-mode task has one;
// an all-mode task one per assignee, a group answered by one of its members.
type TaskResponse struct{ ent.Schema }

func (TaskResponse) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (TaskResponse) Fields() []ent.Field {
	return []ent.Field{
		field.String("task_id").NotEmpty().Immutable(),
		field.String("subject").NotEmpty().Immutable(),
		// Assignee is the entry answered for: the subject, or group:<id>.
		field.String("assignee").NotEmpty().Immutable(),
		field.String("outcome").NotEmpty().Immutable(),
		field.String("comment").Default("").MaxLen(4000).Immutable(),
		field.JSON("input", json.RawMessage{}).Optional().Immutable(),
	}
}
func (TaskResponse) Indexes() []ent.Index {
	return []ent.Index{index.Fields("task_id", "assignee").Unique()}
}
