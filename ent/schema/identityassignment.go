package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityAssignment struct{ ent.Schema }

func (IdentityAssignment) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityAssignment) Fields() []ent.Field {
	return []ent.Field{field.String("role_id").NotEmpty().Immutable().MaxLen(36), field.Enum("principal_type").Values("user", "group").Immutable(), field.String("principal_id").NotEmpty().MaxLen(255).Immutable()}
}
func (IdentityAssignment) Edges() []ent.Edge {
	return []ent.Edge{edge.To("role", IdentityRole.Type).Field("role_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade))}
}
func (IdentityAssignment) Indexes() []ent.Index {
	return []ent.Index{index.Fields("role_id", "principal_type", "principal_id").Unique(), index.Fields("principal_type", "principal_id", "role_id")}
}
