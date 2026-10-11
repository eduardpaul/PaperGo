package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SyncState struct{ ent.Schema }

func (SyncState) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (SyncState) Fields() []ent.Field {
	return []ent.Field{field.String("view_id").NotEmpty().MaxLen(36).Immutable(), field.String("item_id").NotEmpty().MaxLen(36).Immutable(), field.Int64("from_sequence").Positive().Immutable(), field.Int64("to_sequence").Optional().Nillable().Positive(), field.String("fingerprint").NotEmpty().MaxLen(64).Immutable()}
}
func (SyncState) Edges() []ent.Edge {
	return []ent.Edge{edge.To("view", SyncView.Type).Field("view_id").Unique().Required().Immutable()}
}
func (SyncState) Indexes() []ent.Index {
	return []ent.Index{index.Fields("view_id", "item_id", "from_sequence").Unique(), index.Fields("view_id", "item_id").Unique().Annotations(entsql.IndexWhere("to_sequence IS NULL")), index.Fields("view_id", "from_sequence", "to_sequence")}
}
