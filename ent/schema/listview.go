package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type ListView struct{ ent.Schema }

func (ListView) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ListView) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").Immutable(), field.String("name"), field.JSON("columns", []string{}).Default([]string{}),
		field.JSON("query", json.RawMessage{}), field.String("layout").Default("table"), field.Bool("is_default").Default(false),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (ListView) Edges() []ent.Edge {
	return []ent.Edge{edge.To("container", Resource.Type).Field("container_id").Unique().Required().Immutable()}
}
func (ListView) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "name").Unique(), index.Fields("container_id", "id")}
}
