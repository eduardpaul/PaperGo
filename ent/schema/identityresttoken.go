package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IdentityRESTToken struct{ ent.Schema }

func (IdentityRESTToken) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (IdentityRESTToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("subject").NotEmpty().MaxLen(255).Immutable(),
		field.String("name").NotEmpty().Immutable(),
		field.String("prefix").NotEmpty().Immutable(),
		field.String("secret_hash").NotEmpty().Sensitive().Immutable(),
		field.JSON("scopes", []string{}).Immutable(),
		field.Time("expires_at").Immutable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}
func (IdentityRESTToken) Indexes() []ent.Index {
	return []ent.Index{index.Fields("secret_hash").Unique(), index.Fields("subject", "id")}
}

func (IdentityRESTToken) Edges() []ent.Edge {
	return []ent.Edge{edge.To("user", IdentityUser.Type).Field("subject").Unique().Required().Immutable()}
}
