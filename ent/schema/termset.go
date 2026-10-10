package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type TermSet struct{ ent.Schema }

func (TermSet) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (TermSet) Fields() []ent.Field {
	return []ent.Field{
		field.String("group_id").Immutable(), field.String("key").Immutable(), field.String("name"), field.String("description").Default(""),
		field.Bool("is_open").Default(false), field.Bool("is_keywords").Default(false).Immutable(),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (TermSet) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("group", TermGroup.Type).Ref("term_sets").Field("group_id").Unique().Required().Immutable(),
		edge.To("terms", Term.Type),
	}
}
func (TermSet) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("key").Unique(), index.Fields("group_id", "name").Unique(),
		index.Fields("is_keywords").Unique().StorageKey("termset_keywords").Annotations(entsql.IndexWhere("is_keywords")),
	}
}
