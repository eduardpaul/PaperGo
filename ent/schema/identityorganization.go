package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityOrganization struct{ ent.Schema }

func (IdentityOrganization) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityOrganization) Fields() []ent.Field {
	return []ent.Field{
		field.Bool("singleton").Default(true).Immutable(),
		field.String("identifier").NotEmpty(),
		field.String("display_name").NotEmpty(),
		field.String("description").Default(""),
		field.Int("version").Default(1).Positive(),
	}
}

func (IdentityOrganization) Indexes() []ent.Index {
	return []ent.Index{index.Fields("singleton").Unique()}
}
