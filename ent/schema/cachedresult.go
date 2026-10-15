package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// CachedResult is an expiring, reusable result. Its namespace owns the key
// and payload shape; scope controls which callers share results.
type CachedResult struct{ ent.Schema }

func (CachedResult) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (CachedResult) Fields() []ent.Field {
	return []ent.Field{
		field.String("namespace").NotEmpty().Immutable().MaxLen(64),
		field.String("scope").NotEmpty().Immutable().MaxLen(255),
		field.String("key").NotEmpty().Immutable().MaxLen(4096),
		field.JSON("payload", json.RawMessage{}).SchemaType(map[string]string{dialect.SQLite: "text"}).Immutable(),
		field.Time("used_at").Default(func() time.Time { return time.Now().UTC() }),
		field.Time("expires_at"),
	}
}
func (CachedResult) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("namespace", "scope", "key").Unique(),
		index.Fields("expires_at"),
	}
}
