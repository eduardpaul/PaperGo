package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// Workflow is a workspace's automation: its triggers, condition and flow live
// in immutable WorkflowVersion rows, and CurrentVersion selects the one new
// runs use. A built-in workflow has a BuiltInKey and its parameters.
type Workflow struct{ ent.Schema }

func (Workflow) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Workflow) Fields() []ent.Field {
	return []ent.Field{
		field.Time("updated_at").Default(func() time.Time { return time.Now().UTC() }),
		field.String("workspace_id").NotEmpty().Immutable().MaxLen(36),
		field.String("key").NotEmpty().MaxLen(100).Immutable(),
		field.String("name").NotEmpty().MaxLen(400),
		field.String("description").Default(""),
		field.Bool("enabled").Default(true),
		field.String("builtin_key").Optional().Nillable().Immutable(),
		field.String("collection_id").Optional().Nillable().Immutable().MaxLen(36),
		field.JSON("parameters", map[string]any{}).Default(map[string]any{}),
		field.Int("current_version").Positive(),
		field.String("created_by").Immutable(),
		field.String("updated_by"),
		field.Time("deleted_at").Optional().Nillable(),
		field.Int("version").Default(1).Positive(),
	}
}
func (Workflow) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("workspace_id", "key").Unique().Annotations(entsql.IndexWhere("deleted_at IS NULL")),
		index.Fields("workspace_id", "name").Unique().Annotations(entsql.IndexWhere("deleted_at IS NULL")),
		index.Fields("workspace_id", "id"),
	}
}

// WorkflowVersion is one immutable definition of a workflow. Runs keep the
// version they started with.
type WorkflowVersion struct{ ent.Schema }

func (WorkflowVersion) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (WorkflowVersion) Fields() []ent.Field {
	return []ent.Field{
		field.String("workflow_id").NotEmpty().Immutable().MaxLen(36),
		field.Int("number").Positive().Immutable(),
		field.JSON("definition", json.RawMessage{}).Immutable(),
		field.String("created_by").Immutable(),
	}
}
func (WorkflowVersion) Indexes() []ent.Index {
	return []ent.Index{index.Fields("workflow_id", "number").Unique()}
}

// WorkflowTrigger indexes the triggers of each live workflow's current
// version, so a committed write finds the workflows to start by event type.
// Schedule triggers carry their next occurrence.
type WorkflowTrigger struct{ ent.Schema }

func (WorkflowTrigger) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (WorkflowTrigger) Fields() []ent.Field {
	return []ent.Field{
		field.String("workflow_id").NotEmpty().Immutable().MaxLen(36),
		field.String("workspace_id").NotEmpty().Immutable().MaxLen(36),
		field.String("collection_id").Optional().Nillable().Immutable().MaxLen(36),
		field.String("type").NotEmpty().Immutable(),
		field.Int("position").NonNegative().Immutable(),
		field.Time("next_at").Optional().Nillable(),
	}
}
func (WorkflowTrigger) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("type", "workspace_id", "collection_id"),
		index.Fields("workflow_id"),
		index.Fields("next_at").Annotations(entsql.IndexWhere("next_at IS NOT NULL")),
	}
}

// WorkflowRun indexes runs by workspace, workflow and item. The ID is the
// runner's workflow ID; status, steps and results live in the runner.
type WorkflowRun struct{ ent.Schema }

func (WorkflowRun) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
		field.String("workflow_id").NotEmpty().Immutable().MaxLen(36),
		field.Int("workflow_version").Positive().Immutable(),
		field.String("workspace_id").NotEmpty().Immutable().MaxLen(36),
		field.String("item_id").Optional().Nillable().Immutable().MaxLen(36),
		field.String("event_id").NotEmpty().Immutable(),
		field.String("event_type").NotEmpty().Immutable(),
		field.Int("depth").NonNegative().Immutable(),
		field.String("actor").Immutable(),
		field.String("retry_of").Optional().Nillable().Immutable(),
	}
}
func (WorkflowRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("workflow_id", "event_id").Unique(),
		index.Fields("workspace_id", "created_at", "id"),
		index.Fields("workflow_id", "created_at", "id"),
		index.Fields("item_id", "created_at", "id"),
		index.Fields("created_at"),
	}
}

// WorkflowRunItem is the ordered membership of a selection run: one run
// over several items, the first or chosen one being the run's item.
type WorkflowRunItem struct{ ent.Schema }

func (WorkflowRunItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.String("run_id").NotEmpty().Immutable(),
		field.String("item_id").NotEmpty().Immutable().MaxLen(36),
		field.Int("position").NonNegative().Immutable(),
	}
}
func (WorkflowRunItem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "position").Unique(),
		index.Fields("run_id", "item_id").Unique(),
		index.Fields("item_id"),
	}
}
