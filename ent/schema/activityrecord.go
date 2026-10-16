package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ActivityRecord is durable extension-owned state, independent of result caching
// and workflow wait retention. Scope is an executor/queue partition.
type ActivityRecord struct{ ent.Schema }

func (ActivityRecord) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ActivityRecord) Fields() []ent.Field {
	return []ent.Field{
		field.String("namespace").NotEmpty().MaxLen(64), field.String("scope").NotEmpty().MaxLen(255), field.String("key").NotEmpty().MaxLen(128),
		field.String("owner_id").Default(""), field.JSON("payload", json.RawMessage{}), field.String("state").Default("pending"),
		field.Time("updated_at").Default(time.Now), field.Time("expires_at").Optional().Nillable(),
		field.String("lease_owner").Optional().Nillable(), field.String("lease_token").Optional().Nillable(), field.Time("lease_until").Optional().Nillable(),
	}
}
func (ActivityRecord) Indexes() []ent.Index {
	return []ent.Index{index.Fields("namespace", "scope", "key").Unique(), index.Fields("namespace", "scope", "state", "key"), index.Fields("expires_at")}
}

// ActivityReference keeps a payload alive while a wait or submitted batch needs it.
type ActivityReference struct{ ent.Schema }

func (ActivityReference) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ActivityReference) Fields() []ent.Field {
	return []ent.Field{field.String("owner_id").NotEmpty(), field.String("record_id").NotEmpty()}
}
func (ActivityReference) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_id", "record_id").Unique(), index.Fields("record_id")}
}

// BlobLease pins bytes for either an executing activity or a durable wait.
type BlobLease struct{ ent.Schema }

func (BlobLease) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (BlobLease) Fields() []ent.Field {
	return []ent.Field{field.String("owner_id").NotEmpty(), field.String("run_id").NotEmpty(), field.String("blob_id").NotEmpty()}
}
func (BlobLease) Indexes() []ent.Index {
	return []ent.Index{index.Fields("owner_id", "blob_id").Unique(), index.Fields("blob_id"), index.Fields("run_id")}
}
