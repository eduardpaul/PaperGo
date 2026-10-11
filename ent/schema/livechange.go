package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// LiveChange is a short-lived notification position. It contains identities,
// never content snapshots or object references, and survives source deletion.
type LiveChange struct{ ent.Schema }

func (LiveChange) Fields() []ent.Field {
	return []ent.Field{field.Int64("id").Positive().Immutable(), field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(), field.Enum("kind").Values("item", "operation", "run", "access").Immutable(), field.String("entity_id").NotEmpty().MaxLen(200).Immutable(), field.String("action").NotEmpty().Immutable()}
}
func (LiveChange) Indexes() []ent.Index { return []ent.Index{index.Fields("created_at")} }
