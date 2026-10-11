package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"time"
)

type RelationshipType struct{ ent.Schema }

func (RelationshipType) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (RelationshipType) Fields() []ent.Field {
	return []ent.Field{
		field.String("inverse_label").Default(""), field.Bool("directed").Default(false).Immutable(),
		field.Int("max_incoming").Optional().Nillable().Positive(), field.Int("max_outgoing").Optional().Nillable().Positive(), field.JSON("attributes", json.RawMessage{}),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (RelationshipType) Edges() []ent.Edge {
	return []ent.Edge{edge.To("relationships", Relationship.Type)}
}
