package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type BusinessKey struct{ ent.Schema }

func (BusinessKey) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (BusinessKey) Fields() []ent.Field {
	return []ent.Field{field.String("container_id").Immutable(), field.String("item_id").Immutable(), field.String("field_key").Immutable(), field.String("value").Immutable()}
}
func (BusinessKey) Edges() []ent.Edge {
	return []ent.Edge{edge.To("item", Resource.Type).Field("item_id").Unique().Required().Immutable(), edge.To("container", Resource.Type).Field("container_id").Unique().Required().Immutable()}
}
func (BusinessKey) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "field_key", "value").Unique(), index.Fields("item_id")}
}
