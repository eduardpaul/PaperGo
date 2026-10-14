package schema

import (
 "encoding/json"
 "entgo.io/ent"
 "entgo.io/ent/schema/field"
)

// IntegrationRevisionData is immutable provider data pinned to a core revision.
// Providers own its JSON shape; core items, revisions and surfaces stay generic.
type IntegrationRevisionData struct{ ent.Schema }
func (IntegrationRevisionData) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IntegrationRevisionData) Fields() []ent.Field {
 return []ent.Field{
  field.String("revision_id").NotEmpty().Unique().Immutable(),
  field.String("provider_kind").NotEmpty().MaxLen(64).Immutable(),
  field.JSON("data", json.RawMessage{}).Immutable(),
 }
}
