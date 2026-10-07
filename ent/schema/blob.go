package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Blob holds immutable object metadata; bytes live behind the storage port.
type Blob struct{ ent.Schema }

func (Blob) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Blob) Fields() []ent.Field {
	return []ent.Field{
		field.String("item_id").NotEmpty().Immutable().MaxLen(36),
		field.Int("version").Positive().Immutable(),
		field.String("object_key").NotEmpty().Unique().Immutable(),
		field.String("filename").NotEmpty().MaxLen(255).Immutable(),
		field.String("content_type").NotEmpty().MaxLen(255).Immutable(),
		field.Int64("size").NonNegative().Immutable(),
		field.String("sha256").MinLen(64).MaxLen(64).Immutable(),
	}
}
func (Blob) Edges() []ent.Edge {
	return []ent.Edge{edge.From("item", Resource.Type).Ref("blobs").Field("item_id").Unique().Required().Immutable()}
}
func (Blob) Indexes() []ent.Index { return []ent.Index{index.Fields("item_id", "version").Unique()} }
