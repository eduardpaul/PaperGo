package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityPreference struct{ ent.Schema }

func (IdentityPreference) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityPreference) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("owner_type").Values("user", "organization").Immutable(),
		field.String("owner_id").MaxLen(255).Immutable(),
		field.JSON("values", map[string]string{}).Default(map[string]string{}),
		field.Int("version").Default(1).Positive(),
	}
}
func (IdentityPreference) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_type", "owner_id").Unique()}
}
