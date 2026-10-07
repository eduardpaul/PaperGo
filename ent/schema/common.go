package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
	"github.com/google/uuid"
	"time"
)

type Identity struct{ mixin.Schema }

func (Identity) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").DefaultFunc(uuid.NewString).Immutable().MaxLen(36),
		field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
	}
}
