package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type FieldValue struct{ ent.Schema }

func (FieldValue) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (FieldValue) Fields() []ent.Field {
	return []ent.Field{
		field.String("surface_id").Immutable(),
		field.String("container_id").Immutable(),
		field.String("item_id").Immutable(),
		field.Enum("surface").Values("head", "published").Immutable(),
		field.String("field_key").Immutable(),
		field.Enum("field_type").Values("text", "number", "integer", "decimal", "boolean", "datetime", "choice", "note", "email", "url", "date", "lookup", "term", "keywords").Immutable(),
		field.Int("ordinal").Default(0).NonNegative().Immutable(),
		field.Int("scale").Default(0).Immutable(),
		field.String("value_text").Optional().Nillable(),
		field.Int64("value_integer").Optional().Nillable(),
		field.Float("value_number").Optional().Nillable(),
		field.Bool("value_boolean").Optional().Nillable(),
	}
}
func (FieldValue) Edges() []ent.Edge {
	return []ent.Edge{edge.To("item_surface", ItemSurface.Type).Field("surface_id").Unique().Required().Immutable()}
}
func (FieldValue) Indexes() []ent.Index {
	return []ent.Index{index.Fields("surface_id", "field_key", "ordinal").Unique(), index.Fields("container_id", "surface", "field_key", "value_integer", "item_id"), index.Fields("container_id", "surface", "field_key", "value_text", "item_id"), index.Fields("container_id", "surface", "field_key", "value_number", "item_id"), index.Fields("container_id", "surface", "field_key", "value_boolean", "item_id")}
}
