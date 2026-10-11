package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type IdentityUser struct{ ent.Schema }

func (IdentityUser) Fields() []ent.Field {
	return []ent.Field{
		field.String("display_name").Default(""),
		field.String("email").Default(""),
		field.String("sync_hash").Default(""),
		field.Time("deleted_at").Optional().Nillable(),
		field.Bool("active").Default(true),
		field.String("id").NotEmpty().MaxLen(255).Immutable(),
		field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
	}
}
