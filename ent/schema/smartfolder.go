package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// SmartFolder stores a live query, never membership or physical containment.
type SmartFolder struct{ ent.Schema }

func (SmartFolder) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SmartFolder) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").Optional().Nillable().Immutable().MaxLen(36),
		field.String("owner_id").Optional().Nillable().Immutable().MaxLen(255),
		field.String("name").NotEmpty().MaxLen(255), field.String("description").Default(""),
		field.JSON("definition", json.RawMessage{}),
		field.String("created_by").Immutable(), field.String("updated_by"),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Int("version").Default(1).Positive(),
	}
}
func (SmartFolder) Edges() []ent.Edge {
	return []ent.Edge{edge.To("workspace", Resource.Type).Field("workspace_id").Unique().Immutable()}
}
func (SmartFolder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("owner_id", "id"), index.Fields("workspace_id", "id"),
		index.Fields("workspace_id", "name").Unique().Annotations(entsql.IndexWhere("owner_id IS NULL")),
		index.Fields("owner_id", "workspace_id", "name").Unique(),
		index.Fields("owner_id", "name").Unique().Annotations(entsql.IndexWhere("workspace_id IS NULL")),
	}
}
