package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// TermGroup organizes a workspace's term sets. Each workspace has one system
// group, which holds its keywords set.
type TermGroup struct{ ent.Schema }

func (TermGroup) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (TermGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").Immutable(), field.String("name"), field.String("description").Default(""),
		field.Bool("is_system").Default(false).Immutable(),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (TermGroup) Edges() []ent.Edge {
	return []ent.Edge{edge.To("workspace", Resource.Type).Field("workspace_id").Unique().Required().Immutable(), edge.To("term_sets", TermSet.Type)}
}
func (TermGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("workspace_id", "name").Unique(),
		index.Fields("workspace_id").Unique().StorageKey("termgroup_workspace_id_system").Annotations(entsql.IndexWhere("is_system")),
	}
}
