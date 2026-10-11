package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
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
		field.String("container_id").Optional().Immutable(),
		field.String("field_id").Default("").Immutable(),
		field.String("kind").NotEmpty().MaxLen(64).Immutable(),
		field.String("trace_id").DefaultFunc(uuid.NewString).Immutable(),
		field.String("credential_id").Optional().Immutable().StructTag(`json:"-"`),
		field.JSON("credential_scopes", []string{}).Default([]string{}).Immutable().StructTag(`json:"-"`),
		field.JSON("result", json.RawMessage{}).Default(json.RawMessage(`{}`)),
		field.Time("started_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
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
		field.Time("updated_at").Default(func() time.Time { return time.Now().UTC() }).UpdateDefault(func() time.Time { return time.Now().UTC() }),
	}
}
func (Operation) Edges() []ent.Edge {
	return []ent.Edge{edge.To("container", Resource.Type).Field("container_id").Unique().Immutable()}
}
func (Operation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("status", "created_at"), index.Fields("container_id", "id"), index.Fields("status", "updated_at")}
}
