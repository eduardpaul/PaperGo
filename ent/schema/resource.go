package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"papergo/internal/model"
	"time"
)

// Resource is the shared ACL and containment boundary for all DMS content.
type Resource struct{ ent.Schema }

func (Resource) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (Resource) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().MaxLen(36),
		field.String("personal_owner_id").Optional().Nillable().MaxLen(255),
		field.String("system_key").Optional().Nillable().MaxLen(32),
		// Folders and items can move within their collection; triggers keep containment valid.
		field.String("parent_id").Optional().Nillable().MaxLen(36),
		field.String("container_id").Optional().Nillable().MaxLen(36),
		field.String("content_type_id").Optional().Nillable().MaxLen(36),
		field.Enum("kind").Values("workspace", "list", "library", "folder", "item").Immutable(),
		field.String("name").NotEmpty().MaxLen(4096).Validate(model.ValidateTitle),
		// Descriptive metadata of a workspace or collection.
		field.String("description").Default("").MaxRuneLen(2000),
		// Collections only: controls creation of physical folders.
		field.Bool("allow_folders").Default(true),
		field.JSON("tags", []string{}).Default([]string{}),
		// Keys of schema templates explicitly adopted into a collection.
		field.JSON("template_keys", []string{}).Default([]string{}),
		field.JSON("values", map[string]any{}).Default(map[string]any{}),
		field.Bool("inherit_permissions").Default(true),
		field.Int("version").Default(1).Positive(),
		field.String("head_revision_id").Optional().Nillable(),
		field.String("published_revision_id").Optional().Nillable(),
		field.String("schema_head_id").Optional().Nillable(),
		field.Int("next_revision_number").Default(1).Positive(),
		field.Bool("publishing_enabled").Default(false),
		field.Enum("versioning").Values("off", "major").Default("off"),
		field.Int("max_versions").Default(50).Min(1).Max(500),
		// Libraries only: exposes the library's folders and files over WebDAV.
		field.Bool("webdav_enabled").Default(false),
		field.Enum("duplicate_policy").Values("allow", "warn", "block").Default("warn"),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.String("created_by").Default("").Immutable(),
		field.String("updated_by").Default(""),
		// Nearest exclusive ACL scope, maintained by database triggers; never set by the API.
		field.String("scope_id").Optional().Nillable().MaxLen(36),
		// Case-folded head name of a live library folder or item. Its unique index
		// gives library siblings file-system names; other resources leave it null.
		field.String("name_key").Optional().Nillable().MaxLen(4096).StructTag(`json:"-"`),
		// Deleted resources are tombstones: hidden everywhere, their history retained.
		field.Time("deleted_at").Optional().Nillable().StructTag(`json:"-"`),
		field.String("deleted_by").Optional().Nillable().StructTag(`json:"-"`),
	}
}
func (Resource) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("children", Resource.Type),
		edge.To("personal_owner", IdentityUser.Type).Field("personal_owner_id").Unique().Annotations(entsql.OnDelete(entsql.NoAction)),
		edge.From("parent", Resource.Type).Ref("children").Field("parent_id").Unique(),
		edge.To("contained_items", Resource.Type),
		edge.From("container", Resource.Type).Ref("contained_items").Field("container_id").Unique(),
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
		index.Fields("personal_owner_id").Unique().Annotations(entsql.IndexWhere("personal_owner_id IS NOT NULL")),
		index.Fields("workspace_id", "system_key").Unique().Annotations(entsql.IndexWhere("system_key IS NOT NULL")),
		index.Fields("kind", "id", "workspace_id", "scope_id"),
		// scope_id makes these covering for permission checks, so rows a subject
		// cannot read are rejected without loading them.
		index.Fields("workspace_id", "id", "scope_id"),
		index.Fields("parent_id", "id", "scope_id"),
		// Lists one kind of child (the folders of a large library) without scanning the rest.
		index.Fields("parent_id", "kind", "id", "scope_id"),
		index.Fields("container_id", "id"),
		index.Fields("container_id", "content_type_id", "id"),
		// Lets auto-surface queries list a collection's few ACL scopes by loose index scan.
		index.Fields("container_id", "scope_id"),
		index.Fields("parent_id", "name_key").Unique().Annotations(entsql.IndexWhere("name_key IS NOT NULL")),
		// Revision collection asks whether anything still points at a revision.
		index.Fields("head_revision_id").Annotations(entsql.IndexWhere("head_revision_id IS NOT NULL")),
		index.Fields("published_revision_id").Annotations(entsql.IndexWhere("published_revision_id IS NOT NULL")),
	}
}
