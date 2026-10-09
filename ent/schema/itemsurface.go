package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ItemSurface is derived data; immutable revisions are authoritative.
type ItemSurface struct{ ent.Schema }

func (ItemSurface) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (ItemSurface) Fields() []ent.Field {
	return []ent.Field{
		field.String("item_id").Immutable(),
		field.String("container_id").Immutable(),
		field.String("workspace_id").Immutable(),
		field.Enum("surface").Values("head", "published").Immutable(),
		field.String("revision_id"),
		field.String("name"),
		field.JSON("tags", []string{}),
		field.JSON("payload", json.RawMessage{}),
		// Fixed UTC strings retain nanoseconds and compare chronologically in SQL.
		field.String("item_created_at"),
		field.String("item_created_by"),
		field.String("modified_at"),
		field.String("modified_by"),
	}
}
func (ItemSurface) Edges() []ent.Edge {
	return []ent.Edge{edge.To("item", Resource.Type).Field("item_id").Unique().Required().Immutable(), edge.To("revision", ItemRevision.Type).Field("revision_id").Unique().Required()}
}
func (ItemSurface) Indexes() []ent.Index {
	return []ent.Index{index.Fields("item_id", "surface").Unique(), index.Fields("workspace_id", "surface", "item_id"), index.Fields("container_id", "surface", "item_id"), index.Fields("container_id", "surface", "modified_at", "item_id"), index.Fields("container_id", "surface", "modified_by", "item_id"), index.Fields("container_id", "surface", "item_created_at", "item_id"), index.Fields("container_id", "surface", "item_created_by", "item_id"), index.Fields("container_id", "surface", "name", "item_id")}
}
