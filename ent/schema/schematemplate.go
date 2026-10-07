package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type SchemaTemplate struct{ ent.Schema }

func (SchemaTemplate) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SchemaTemplate) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").Immutable(), field.String("key").Immutable(), field.String("name"), field.String("description").Default(""),
		field.JSON("definition", json.RawMessage{}), field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (SchemaTemplate) Edges() []ent.Edge {
	return []ent.Edge{edge.To("workspace", Resource.Type).Field("workspace_id").Unique().Required().Immutable()}
}
func (SchemaTemplate) Indexes() []ent.Index {
	return []ent.Index{index.Fields("workspace_id", "key").Unique()}
}
