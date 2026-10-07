package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SchemaRevision struct{ ent.Schema }

func (SchemaRevision) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SchemaRevision) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").Immutable(),
		field.Int("revision_number").Positive().Immutable(),
		field.JSON("definition", json.RawMessage{}).Immutable(),
		field.String("created_by").NotEmpty().Immutable(),
	}
}
func (SchemaRevision) Edges() []ent.Edge {
	return []ent.Edge{edge.From("container", Resource.Type).Ref("schema_revisions").Field("container_id").Unique().Required().Immutable(), edge.To("items", ItemRevision.Type)}
}
func (SchemaRevision) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "revision_number").Unique(), index.Fields("container_id", "id").Unique()}
}
