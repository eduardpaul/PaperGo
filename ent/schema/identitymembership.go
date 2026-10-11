package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityMembership struct{ ent.Schema }

func (IdentityMembership) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityMembership) Fields() []ent.Field {
	return []ent.Field{field.String("group_id").NotEmpty().Immutable().MaxLen(36), field.Enum("principal_type").Values("user", "group").Immutable(), field.String("principal_id").NotEmpty().MaxLen(255).Immutable()}
}
func (IdentityMembership) Edges() []ent.Edge {
	return []ent.Edge{edge.To("group", IdentityGroup.Type).Field("group_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (IdentityMembership) Indexes() []ent.Index {
	return []ent.Index{index.Fields("group_id", "principal_type", "principal_id").Unique(), index.Fields("principal_type", "principal_id", "group_id")}
}
