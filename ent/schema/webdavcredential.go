package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WebDAVCredential is a revocable app password for clients, such as Windows
// Explorer, that cannot present OIDC bearer tokens. Only its SHA-256 is stored.
type WebDAVCredential struct{ ent.Schema }

func (WebDAVCredential) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "webdav_credentials"}}
}
func (WebDAVCredential) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (WebDAVCredential) Fields() []ent.Field {
	return []ent.Field{
		field.String("subject").NotEmpty().MaxLen(255).Immutable(),
		// The service limits labels to 100 characters; MaxLen counts bytes.
		field.String("label").NotEmpty().MaxLen(400).Immutable(),
		field.String("secret_hash").MinLen(64).MaxLen(64).Unique().Immutable().Sensitive(),
		field.Time("expires_at").Optional().Nillable().Immutable(),
	}
}
func (WebDAVCredential) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subject", "id")}
}
