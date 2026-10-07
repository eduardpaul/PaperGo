package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Publication stores an immutable snapshot. Publishing never grants public access.
type Publication struct{ ent.Schema }

func (Publication) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Publication) Fields() []ent.Field {
	return []ent.Field{
		field.String("item_id").NotEmpty().Immutable().MaxLen(36),
		field.Int("version").Positive().Immutable(),
		field.Enum("action").Values("publish", "unpublish").Default("publish").Immutable(),
		field.String("revision_id").Optional().Nillable().Immutable(),
		field.String("published_by").NotEmpty().Immutable().MaxLen(255),
		field.JSON("snapshot", json.RawMessage{}).Immutable(),
	}
}
func (Publication) Edges() []ent.Edge {
	return []ent.Edge{edge.From("item", Resource.Type).Ref("publications").Field("item_id").Unique().Required().Immutable(), edge.To("revision", ItemRevision.Type).Field("revision_id").Unique().Immutable()}
}
func (Publication) Indexes() []ent.Index {
	return []ent.Index{index.Fields("item_id", "version", "action").Unique()}
}
