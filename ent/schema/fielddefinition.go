package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"papergo/internal/model"
)

type FieldDefinition struct{ ent.Schema }

func (FieldDefinition) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (FieldDefinition) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").NotEmpty().Immutable().MaxLen(36),
		field.String("key").NotEmpty().MaxLen(64).Immutable(),
		field.String("label").NotEmpty().MaxLen(255),
		field.Enum("type").Values("text", "number", "integer", "decimal", "boolean", "datetime", "choice", "note", "email", "url", "date", "lookup", "term"),
		field.JSON("options", model.FieldOptions{}).Default(model.FieldOptions{}),
		field.Bool("indexed").Default(false),
		field.Int("scale").Default(0).Min(0).Max(9),
		field.Bool("required").Default(false),
		field.JSON("choices", []string{}).Default([]string{}),
	}
}
func (FieldDefinition) Edges() []ent.Edge {
	return []ent.Edge{edge.From("container", Resource.Type).Ref("definitions").Field("container_id").Unique().Required().Immutable()}
}
func (FieldDefinition) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "key").Unique()}
}
