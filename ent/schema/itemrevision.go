package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ItemRevision struct{ ent.Schema }

func (ItemRevision) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ItemRevision) Fields() []ent.Field {
	return []ent.Field{
		field.String("item_id").Immutable(),
		field.String("container_id").Immutable(),
		field.String("schema_revision_id").Immutable(),
		field.String("blob_id").Optional().Nillable().Immutable(),
		field.Int("revision_number").Positive().Immutable(),
		field.String("name").NotEmpty().MaxLen(255).Immutable(),
		field.JSON("tags", []string{}).Immutable(),
		field.JSON("payload", json.RawMessage{}).Immutable(),
		field.String("created_by").NotEmpty().Immutable(),
	}
}
func (ItemRevision) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("item", Resource.Type).Ref("revisions").Field("item_id").Unique().Required().Immutable(),
		edge.From("schema_revision", SchemaRevision.Type).Ref("items").Field("schema_revision_id").Unique().Required().Immutable(),
		edge.To("blob", Blob.Type).Field("blob_id").Unique().Immutable(),
	}
}
func (ItemRevision) Indexes() []ent.Index {
	return []ent.Index{index.Fields("item_id", "revision_number").Unique(), index.Fields("item_id", "id").Unique()}
}
