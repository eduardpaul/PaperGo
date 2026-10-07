package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Relationship struct{ ent.Schema }

func (Relationship) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Relationship) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable().MaxLen(36),
		field.String("source_id").NotEmpty().Immutable().MaxLen(36),
		field.String("target_id").NotEmpty().Immutable().MaxLen(36),
		field.String("name").NotEmpty().MaxLen(64),
		field.String("inverse_name").Optional().MaxLen(64),
		field.String("type_id").Optional().Nillable().Immutable(),
		field.Bool("directed").Default(true).Immutable(),
		field.Int("version").Default(1).Positive(),
		field.JSON("metadata", map[string]any{}).Default(map[string]any{}),
	}
}
func (Relationship) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("relationship_type", RelationshipType.Type).Ref("relationships").Field("type_id").Unique().Immutable(),
		edge.From("source", Resource.Type).Ref("outgoing").Field("source_id").Unique().Required().Immutable(),
		edge.From("target", Resource.Type).Ref("incoming").Field("target_id").Unique().Required().Immutable(),
	}
}
func (Relationship) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("type_id", "source_id", "target_id").Unique(),
		index.Fields("type_id", "target_id"),
		index.Fields("source_id", "name", "target_id").Unique(),
		index.Fields("source_id", "name", "id"),
		index.Fields("source_id", "id"),
		index.Fields("target_id", "name", "id"),
		index.Fields("target_id", "id"),
		index.Fields("target_id", "inverse_name", "id"),
	}
}
