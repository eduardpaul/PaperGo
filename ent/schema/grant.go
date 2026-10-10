package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Grant struct{ ent.Schema }

func (Grant) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Grant) Fields() []ent.Field {
	return []ent.Field{
		field.String("resource_id").NotEmpty().Immutable().MaxLen(36),
		field.Enum("principal_type").Values("user", "group", "workspace_visitor", "workspace_member", "workspace_owner"),
		field.String("principal_id").NotEmpty().MaxLen(255),
		field.Enum("action").Values("read", "read_draft", "write", "publish", "manage"),
		field.Enum("effect").Values("allow").Default("allow"),
	}
}
func (Grant) Edges() []ent.Edge {
	return []ent.Edge{edge.From("resource", Resource.Type).Ref("grants").Field("resource_id").Unique().Required().Immutable()}
}
func (Grant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("resource_id", "principal_type", "principal_id", "action").Unique(), index.Fields("principal_type", "principal_id", "resource_id")}
}
