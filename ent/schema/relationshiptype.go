package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type RelationshipType struct{ ent.Schema }

func (RelationshipType) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (RelationshipType) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").Immutable(), field.String("key").Immutable(), field.String("label"), field.String("inverse_label").Default(""), field.Bool("directed").Default(true).Immutable(),
		field.Int("max_incoming").Optional().Nillable().Positive(), field.Int("max_outgoing").Optional().Nillable().Positive(), field.JSON("attributes", json.RawMessage{}),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (RelationshipType) Edges() []ent.Edge {
	return []ent.Edge{edge.To("workspace", Resource.Type).Field("workspace_id").Unique().Required().Immutable(), edge.To("relationships", Relationship.Type)}
}
func (RelationshipType) Indexes() []ent.Index {
	return []ent.Index{index.Fields("workspace_id", "key").Unique()}
}
