package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ApprovalRequest is a decision a workflow run waits for: an approval node
// asks its assignees to approve or reject, optionally with a form and a
// review of a staged file. While nobody has decided, the run is parked: it
// holds no worker, and a decision resumes it.
type ApprovalRequest struct{ ent.Schema }

func (ApprovalRequest) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ApprovalRequest) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable(),
		field.String("workflow_id").NotEmpty().Immutable(),
		field.String("run_id").NotEmpty().Immutable(),
		field.String("node").NotEmpty().Immutable(),
		field.String("item_id").Optional().Nillable().Immutable(),
		field.String("title").NotEmpty().MaxLen(500).Immutable(),
		field.String("description").Default("").MaxLen(4000).Immutable(),
		field.JSON("assignees", []string{}).Immutable(),
		field.JSON("input_schema", map[string]any{}).Optional().Immutable(),
		field.JSON("review", map[string]any{}).Optional().Immutable(),
		field.Enum("status").Values("open", "approved", "rejected", "cancelled").Default("open"),
		// Parked marks a run that stopped to wait for this decision.
		field.Bool("parked").Default(false),
		field.String("decided_by").Optional().Nillable(),
		field.Time("decided_at").Optional().Nillable(),
		field.String("comment").Default("").MaxLen(4000),
		field.JSON("input", json.RawMessage{}).Optional(),
		field.String("created_by").Immutable(),
		field.Time("updated_at").Default(func() time.Time { return time.Now().UTC() }).UpdateDefault(func() time.Time { return time.Now().UTC() }),
	}
}
func (ApprovalRequest) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id"), index.Fields("status", "workspace_id"), index.Fields("parked", "status")}
}
