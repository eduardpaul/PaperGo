package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OperationItem holds the frozen selection still to process. Completion removes
// the row in the same transaction as its item edit and operation counters.
type OperationItem struct{ ent.Schema }

func (OperationItem) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (OperationItem) Fields() []ent.Field {
	return []ent.Field{
		field.String("operation_id").Immutable(), field.String("item_id").Immutable(),
	}
}
func (OperationItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("operation", Operation.Type).Field("operation_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("item", Resource.Type).Field("item_id").Unique().Required().Immutable(),
	}
}
func (OperationItem) Indexes() []ent.Index {
	return []ent.Index{index.Fields("operation_id", "item_id").Unique()}
}
