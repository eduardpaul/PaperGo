package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityGroup struct{ ent.Schema }

func (IdentityGroup) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(200),
		field.String("description").Default("").MaxLen(2000),
		field.Enum("source").Values("local", "oidc").Default("local"),
		field.Int("version").Default(1).Positive(),
	}
}
func (IdentityGroup) Indexes() []ent.Index { return []ent.Index{index.Fields("name").Unique()} }
