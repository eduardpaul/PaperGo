package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// BlobDeletion persists object cleanup after permanent item deletion.
type BlobDeletion struct{ ent.Schema }

func (BlobDeletion) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (BlobDeletion) Fields() []ent.Field {
	return []ent.Field{field.String("object_key").NotEmpty().Immutable().Unique()}
}
