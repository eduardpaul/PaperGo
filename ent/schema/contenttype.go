package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"papergo/internal/model"
	"time"
)

type ContentType struct{ ent.Schema }

func (ContentType) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ContentType) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").Immutable(), field.String("key").Immutable().MaxLen(64), field.String("name").NotEmpty().MaxLen(255),
		field.JSON("field_keys", []string{}).Default([]string{}), field.JSON("rules", []model.ValidationRule{}).Default([]model.ValidationRule{}),
		field.Bool("is_default").Default(false), field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (ContentType) Edges() []ent.Edge {
	return []ent.Edge{edge.To("container", Resource.Type).Field("container_id").Unique().Required().Immutable()}
}
func (ContentType) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "key").Unique(), index.Fields("container_id", "id").Unique(), index.Fields("container_id").Unique().Annotations(entsql.IndexWhere("is_default=1"))}
}
