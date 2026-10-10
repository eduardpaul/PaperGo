package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"time"
)

type IdentityUser struct{ ent.Schema }

func (IdentityUser) Fields() []ent.Field {
	return []ent.Field{field.String("id").NotEmpty().MaxLen(255).Immutable(), field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable()}
}
