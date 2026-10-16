package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BlobPreview is an immutable rendition shared by identical source bytes.
type BlobPreview struct{ ent.Schema }

func (BlobPreview) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (BlobPreview) Fields() []ent.Field {
	return []ent.Field{
		field.String("sha256").NotEmpty().Immutable(),
		field.Enum("status").Values("ready", "failed"),
		field.String("object_key").Default(""),
		field.String("object_sha256").Default(""),
		field.Int64("object_size").Default(0).Min(0),
		field.Int("width").Default(0).Min(0),
		field.Int("height").Default(0).Min(0),
	}
}
func (BlobPreview) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sha256").Unique(), index.Fields("object_key")}
}
