package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"papergo/internal/model"
	"time"
)

// Operation is a tracked background job. A field_index operation converges one
// field's field_values rows, one surface batch per write transaction, so long
// rebuilds never hold the writer. surface and after_item_id are its resume point.
type Operation struct{ ent.Schema }

func (Operation) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Operation) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").Immutable(),
		field.String("field_id").Default("").Immutable(),
		field.Enum("kind").Values("field_index", "bulk_update").Immutable(),
		field.Enum("status").Values("pending", "succeeded", "failed", "superseded").Default("pending"),
		field.Enum("surface").Values("head", "published").Default("head").StructTag(`json:"-"`),
		field.String("after_item_id").Default("").StructTag(`json:"-"`),
		field.Int("processed").Default(0).Min(0),
		field.JSON("payload", json.RawMessage{}).Default(json.RawMessage(`{}`)).Immutable().StructTag(`json:"-"`),
		field.Bool("prepared").Default(false).StructTag(`json:"-"`),
		field.Int("matched").Default(0).Min(0),
		field.Int("updated").Default(0).Min(0),
		field.Int("failed").Default(0).Min(0),
		field.Int("progress").Default(0).Min(0).Max(100),
		field.JSON("failures", []model.OperationFailure{}).Default([]model.OperationFailure{}),
		field.String("error").Default(""),
		field.String("created_by").Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (Operation) Edges() []ent.Edge {
	return []ent.Edge{edge.To("container", Resource.Type).Field("container_id").Unique().Required().Immutable()}
}
func (Operation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status", "created_at"), index.Fields("container_id", "id")}
}
