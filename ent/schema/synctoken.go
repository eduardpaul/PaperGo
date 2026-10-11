package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SyncToken struct{ ent.Schema }

func (SyncToken) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SyncToken) Fields() []ent.Field {
	return []ent.Field{field.String("view_id").NotEmpty().MaxLen(36).Immutable(), field.Int64("sequence").Positive().Immutable(), field.JSON("changes", json.RawMessage{}).Default(json.RawMessage(`[]`)).StructTag(`json:"-"`), field.Time("page_expires_at").Immutable(), field.Time("expires_at")}
}
func (SyncToken) Edges() []ent.Edge {
	return []ent.Edge{edge.To("view", SyncView.Type).Field("view_id").Unique().Required().Immutable()}
}
func (SyncToken) Indexes() []ent.Index {
	return []ent.Index{index.Fields("view_id", "sequence"), index.Fields("expires_at"), index.Fields("page_expires_at")}
}
