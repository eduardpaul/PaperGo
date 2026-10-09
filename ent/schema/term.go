package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type Term struct{ ent.Schema }

func (Term) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Term) Fields() []ent.Field {
	return []ent.Field{
		field.String("term_set_id").Immutable(), field.String("parent_id").Optional().Nillable().Immutable(), field.String("name"), field.String("normalized_name"),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}), field.JSON("synonyms", []string{}).Default([]string{}), field.Bool("deprecated").Default(false),
		field.Int("version").Default(1).Positive(), field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (Term) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("term_set", TermSet.Type).Ref("terms").Field("term_set_id").Unique().Required().Immutable(),
		edge.To("children", Term.Type), edge.From("parent", Term.Type).Ref("children").Field("parent_id").Unique().Immutable(),
	}
}
func (Term) Indexes() []ent.Index {
	return []ent.Index{index.Fields("term_set_id", "normalized_name").Unique(), index.Fields("term_set_id", "id"), index.Fields("parent_id")}
}
