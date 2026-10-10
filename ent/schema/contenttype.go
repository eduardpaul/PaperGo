package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"papergo/internal/model"
	"time"
)

type ContentType struct{ ent.Schema }

func (ContentType) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ContentType) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Immutable().MaxLen(64), field.String("name").NotEmpty().MaxLen(255),
		field.JSON("field_ids", []string{}).Default([]string{}), field.JSON("rules", []model.ValidationRule{}).Default([]model.ValidationRule{}),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (ContentType) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key")}
}
