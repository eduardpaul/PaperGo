package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ListIntegration is configuration on an ordinary list, never a resource kind.
type ListIntegration struct{ ent.Schema }

func (ListIntegration) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ListIntegration) Fields() []ent.Field {
	return []ent.Field{
		field.String("container_id").NotEmpty().Immutable(),
		field.String("kind").NotEmpty().MaxLen(64).Immutable(),
		field.String("name").NotEmpty().MaxRuneLen(255),
		field.Bool("paused").Default(false),
		field.Int("version").Default(1).Positive(),
		field.String("generation").NotEmpty().StructTag(`json:"-"`),
		field.Bytes("config_ciphertext").Sensitive(),
		field.String("config_hash").Sensitive(),
		field.String("configured_by").NotEmpty().StructTag(`json:"-"`),
		field.String("credential_id").Default("").Sensitive(),
		field.JSON("credential_scopes", []string{}).Default([]string{}).Sensitive(),
		field.JSON("state", json.RawMessage{}).Default(json.RawMessage(`{}`)).Sensitive(),
		field.Time("last_attempt_at").Optional().Nillable(),
		field.Time("last_success_at").Optional().Nillable(),
		field.String("error").Default("").MaxLen(1000),
		field.Int("item_count").Default(0).NonNegative(),
		field.String("lease_token").Default("").Sensitive(),
		field.Time("lease_until").Optional().Nillable().StructTag(`json:"-"`),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}
func (ListIntegration) Indexes() []ent.Index {
	return []ent.Index{index.Fields("container_id", "kind", "config_hash").Unique()}
}
