package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// Term is a node of a term set. Path lists the ids from the root to the term
// ("/root/child/term/"), so a subtree is one index range. Terms are never
// deleted: they are deprecated, or merged into another term.
type Term struct{ ent.Schema }

func (Term) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Term) Fields() []ent.Field {
	return []ent.Field{
		field.String("term_set_id"), field.String("parent_id").Optional().Nillable(), field.String("name"), field.String("normalized_name"),
		field.String("description").Default(""), field.String("color").Optional().Nillable(), field.Int("sort_order").Default(0), field.String("path"),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}), field.JSON("synonyms", []string{}).Default([]string{}),
		field.String("merged_into_id").Optional().Nillable(), field.Bool("available_as_keyword").Default(false), field.Bool("deprecated").Default(false),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (Term) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("term_set", TermSet.Type).Ref("terms").Field("term_set_id").Unique().Required(),
		edge.To("children", Term.Type), edge.From("parent", Term.Type).Ref("children").Field("parent_id").Unique(),
		edge.To("merged", Term.Type), edge.From("merged_into", Term.Type).Ref("merged").Field("merged_into_id").Unique(),
	}
}
func (Term) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("term_set_id", "parent_id", "sort_order", "normalized_name", "id"), index.Fields("term_set_id", "id"), index.Fields("parent_id"), index.Fields("path"),
		index.Fields("merged_into_id").Annotations(entsql.IndexWhere("merged_into_id IS NOT NULL")),
		index.Fields("term_set_id").StorageKey("term_available_as_keyword").Annotations(entsql.IndexWhere("available_as_keyword")),
	}
}
