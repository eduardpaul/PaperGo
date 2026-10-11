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
		field.String("workspace_id").NotEmpty().Optional().Immutable(),
		field.String("resource_id").NotEmpty().Optional().Immutable(),
		field.String("subject").NotEmpty().Immutable(),
		field.String("action").NotEmpty().Immutable(),
		field.String("entity_type").NotEmpty().Immutable(),
		field.String("entity_id").Optional().Immutable(),
		field.JSON("properties", []string{}).Default([]string{}).Immutable(),
		field.String("trace_id").NotEmpty().Immutable(),
		field.JSON("details", map[string]any{}).Default(map[string]any{}).Immutable(),
	}
}
func (AuditEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("workspace_id", "created_at"), index.Fields("resource_id", "created_at"), index.Fields("created_at", "id"), index.Fields("subject", "created_at"), index.Fields("entity_type", "entity_id", "created_at")}
}
