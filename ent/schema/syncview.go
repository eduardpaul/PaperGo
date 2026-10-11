package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SyncView struct{ ent.Schema }

func (SyncView) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SyncView) Fields() []ent.Field {
	return []ent.Field{field.String("subject").NotEmpty().MaxLen(255).Immutable(), field.String("container_id").NotEmpty().MaxLen(36).Immutable(), field.Int64("sequence").Default(0).NonNegative()}
}
func (SyncView) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subject", "container_id").Unique()}
}
