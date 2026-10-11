package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CollectionType attaches an organization definition without copying it.
type CollectionType struct{ ent.Schema }

func (CollectionType) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (CollectionType) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").Immutable(), field.String("content_type_id").Immutable(), field.Bool("is_default").Default(false),
	}
}
func (CollectionType) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("container", Resource.Type).Field("container_id").Unique().Required().Immutable(),
		edge.To("content_type", ContentType.Type).Field("content_type_id").Unique().Required().Immutable(),
	}
}
func (CollectionType) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("container_id", "content_type_id").Unique(),
		index.Fields("content_type_id", "container_id"),
		index.Fields("container_id").Unique().Annotations(entsql.IndexWhere("is_default=1")),
	}
}
