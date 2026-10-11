package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// DomainEvent is the durable log of what committed writes did, written in
// the same transaction as the change. The runner's dispatcher, on any node,
// matches undispatched events to workflows and starts their runs.
type DomainEvent struct{ ent.Schema }

func (DomainEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").Immutable(),
		field.Time("created_at").Default(func() time.Time { return time.Now().UTC() }).Immutable(),
		field.String("type").NotEmpty().Immutable(),
		field.String("workspace_id").NotEmpty().Immutable(),
		field.String("collection_id").Optional().Nillable().Immutable(),
		field.String("resource_id").Optional().Nillable().Immutable(),
		field.String("actor").Immutable(),
		field.JSON("data", map[string]any{}).Default(map[string]any{}).Immutable(),
		field.Int("depth").NonNegative().Immutable(),
		// CauseRunID is the workflow run whose step made the change, if any.
		field.String("cause_run_id").Optional().Nillable().Immutable(),
		field.Time("dispatched_at").Optional().Nillable(),
	}
}
func (DomainEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at", "id").Annotations(entsql.IndexWhere("dispatched_at IS NULL")),
		index.Fields("dispatched_at").Annotations(entsql.IndexWhere("dispatched_at IS NOT NULL")),
	}
}
