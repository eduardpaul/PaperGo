package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// TermGroup organizes the organization's shared term sets. The system group holds keywords.
type TermGroup struct{ ent.Schema }

func (TermGroup) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (TermGroup) Fields() []ent.Field {
	return []ent.Field{
		field.String("name"), field.String("description").Default(""),
		field.Bool("is_system").Default(false).Immutable(),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (TermGroup) Edges() []ent.Edge {
	return []ent.Edge{edge.To("term_sets", TermSet.Type)}
}
func (TermGroup) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name").Unique(),
		index.Fields("is_system").Unique().StorageKey("termgroup_system").Annotations(entsql.IndexWhere("is_system")),
	}
}
