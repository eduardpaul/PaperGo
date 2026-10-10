package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityRole struct{ ent.Schema }

func (IdentityRole) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityRole) Fields() []ent.Field {
	return []ent.Field{field.String("name").NotEmpty().MaxLen(200), field.String("description").Default("").MaxLen(2000), field.JSON("scopes", []string{}).Default([]string{}), field.Bool("builtin").Default(false).Immutable(), field.Int("version").Default(1).Positive()}
}
func (IdentityRole) Indexes() []ent.Index { return []ent.Index{index.Fields("name").Unique()} }
