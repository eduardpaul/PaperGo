package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GroupInbox struct{ ent.Schema }

func (GroupInbox) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (GroupInbox) Fields() []ent.Field {
	return []ent.Field{field.String("group_id").NotEmpty().MaxLen(36), field.String("library_id").NotEmpty().MaxLen(36)}
}
func (GroupInbox) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id").Unique(), index.Fields("library_id")}
}

func (GroupInbox) Edges() []ent.Edge {
	return []ent.Edge{edge.To("group", IdentityGroup.Type).Field("group_id").Required().Unique(), edge.To("library", Resource.Type).Field("library_id").Required().Unique()}
}
