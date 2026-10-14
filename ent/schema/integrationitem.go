package schema

import (
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
 "entgo.io/ent/schema/index"
)

// IntegrationItem binds a provider's external identity to an ordinary item.
// Detaching configuration retains the item and its revision data as local content.
type IntegrationItem struct{ ent.Schema }
func (IntegrationItem) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IntegrationItem) Fields() []ent.Field {
 return []ent.Field{
  field.String("item_id").NotEmpty().Unique().Immutable(),
  field.String("integration_id").Optional().Nillable(),
  field.String("provider_kind").NotEmpty().MaxLen(64).Immutable(),
  field.String("external_id").NotEmpty().MaxLen(4096).Immutable().StructTag(`json:"-"`),
  field.JSON("managed_fields", []string{}).Default([]string{}),
 }
}
func (IntegrationItem) Indexes() []ent.Index { return []ent.Index{index.Fields("integration_id", "external_id").Unique()} }
