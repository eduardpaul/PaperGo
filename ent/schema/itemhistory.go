package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ItemHistory retains a revision in user-visible history independently of live
// content and immutable publication snapshots.
type ItemHistory struct{ ent.Schema }

func (ItemHistory) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ItemHistory) Fields() []ent.Field {
	return []ent.Field{
		field.String("revision_id").Immutable(),
		field.JSON("changed_fields", []string{}).Immutable(),
	}
}
func (ItemHistory) Edges() []ent.Edge {
	return []ent.Edge{edge.From("revision", ItemRevision.Type).Ref("history").Field("revision_id").Unique().Required().Immutable()}
}
func (ItemHistory) Indexes() []ent.Index { return []ent.Index{index.Fields("revision_id").Unique()} }
