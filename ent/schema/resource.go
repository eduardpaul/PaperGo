package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// Resource is the shared ACL and containment boundary for all DMS content.
type Resource struct{ ent.Schema }

func (Resource) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Resource) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable().MaxLen(36),
		// Folders and items can move within their collection; triggers keep containment valid.
		field.String("parent_id").Optional().Nillable().MaxLen(36),
		field.String("container_id").Optional().Nillable().Immutable().MaxLen(36),
		field.Enum("kind").Values("workspace", "list", "library", "folder", "item").Immutable(),
		field.String("name").NotEmpty().MaxLen(255),
		field.JSON("tags", []string{}).Default([]string{}),
		field.JSON("values", map[string]any{}).Default(map[string]any{}),
		field.Bool("inherit_permissions").Default(true),
		field.Int("version").Default(1).Positive(),
		field.String("head_revision_id").Optional().Nillable(),
		field.String("published_revision_id").Optional().Nillable(),
		field.String("schema_head_id").Optional().Nillable(),
		field.Int("next_revision_number").Default(1).Positive(),
		field.Bool("publishing_enabled").Default(false),
		// Libraries only: exposes the library's folders and files over WebDAV.
		field.Bool("webdav_enabled").Default(false),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		// Nearest exclusive ACL scope, maintained by database triggers; never set by the API.
		field.String("scope_id").Optional().Nillable().MaxLen(36),
		// Case-folded head name of a live library folder or item. Its unique index
		// gives library siblings file-system names; other resources leave it null.
		field.String("name_key").Optional().Nillable().MaxLen(1024).StructTag(`json:"-"`),
		// Deleted resources are tombstones: hidden everywhere, their history retained.
		field.Time("deleted_at").Optional().Nillable().StructTag(`json:"-"`),
	}
}
func (Resource) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("children", Resource.Type),
		edge.From("parent", Resource.Type).Ref("children").Field("parent_id").Unique(),
		edge.To("contained_items", Resource.Type),
		edge.From("container", Resource.Type).Ref("contained_items").Field("container_id").Unique().Immutable(),
		edge.To("definitions", FieldDefinition.Type),
		edge.To("grants", Grant.Type),
		edge.To("outgoing", Relationship.Type),
		edge.To("incoming", Relationship.Type),
		edge.To("publications", Publication.Type),
		edge.To("blobs", Blob.Type),
		edge.To("revisions", ItemRevision.Type),
		edge.To("schema_revisions", SchemaRevision.Type),
		edge.To("head_revision", ItemRevision.Type).Field("head_revision_id").Unique(),
		edge.To("published_revision", ItemRevision.Type).Field("published_revision_id").Unique(),
		edge.To("schema_head", SchemaRevision.Type).Field("schema_head_id").Unique(),
	}
}
func (Resource) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("workspace_id", "kind", "id"),
		// scope_id makes these covering for permission checks, so rows a subject
		// cannot read are rejected without loading them.
		index.Fields("workspace_id", "id", "scope_id"),
		index.Fields("parent_id", "id", "scope_id"),
		index.Fields("container_id", "id"),
		index.Fields("parent_id", "name_key").Unique().Annotations(entsql.IndexWhere("name_key IS NOT NULL")),
	}
}
