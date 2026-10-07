package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type AuditEvent struct{ ent.Schema }

func (AuditEvent) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (AuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable(),
		field.String("resource_id").NotEmpty().Immutable(),
		field.String("subject").NotEmpty().Immutable(),
		field.String("action").NotEmpty().Immutable(),
		field.JSON("details", map[string]any{}).Default(map[string]any{}).Immutable(),
	}
}
func (AuditEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("workspace_id", "created_at"), index.Fields("resource_id", "created_at")}
}
