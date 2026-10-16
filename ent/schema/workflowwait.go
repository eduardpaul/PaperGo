package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkflowWait is a durable wait: a parked run, a batch request, or coordinator state.
type WorkflowWait struct{ ent.Schema }

func (WorkflowWait) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (WorkflowWait) Fields() []ent.Field {
	return []ent.Field{
		field.Time("updated_at").Default(time.Now),
		field.String("kind").NotEmpty().MaxLen(64),
		field.String("key").NotEmpty().MaxLen(128),
		field.String("workspace_id").NotEmpty().MaxLen(36),
		// Run IDs are wf:<workflow uuid>:<event uuid> (76 characters).
		field.String("run_id").NotEmpty().MaxLen(80),
		field.String("node").NotEmpty().MaxLen(100),
		field.String("execution_id").NotEmpty().MaxLen(128),
		field.JSON("data", json.RawMessage{}).Default(json.RawMessage(`{}`)).SchemaType(map[string]string{dialect.SQLite: "text"}),
		field.JSON("completion", json.RawMessage{}).Optional().SchemaType(map[string]string{dialect.SQLite: "text"}),
		field.Time("deadline").Optional().Nillable(),
		field.String("claim_run_id").Optional().Nillable().MaxLen(80),
		field.String("claim_token").Optional().Nillable().MaxLen(64),
		field.Enum("status").Values("open", "completed", "closed").Default("open"),
		field.Bool("parking").Default(false),
	}
}
func (WorkflowWait) Indexes() []ent.Index {
	return []ent.Index{index.Fields("kind", "key").Unique(), index.Fields("run_id"), index.Fields("workspace_id", "kind")}
}
