package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"papergo/internal/model"
)

// StagedFile is a file a workflow run produced for an item, such as a
// smaller image or a PDF composed from photos, waiting to be promoted to the
// item's current file or discarded. Its bytes stay referenced while it is
// pending. Sources are the file versions it was made from; promotion
// requires each to still be its item's current file.
type StagedFile struct{ ent.Schema }

func (StagedFile) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (StagedFile) Fields() []ent.Field {
	return []ent.Field{
		field.String("workspace_id").NotEmpty().Immutable(),
		field.String("item_id").NotEmpty().Immutable(),
		field.String("run_id").NotEmpty().Immutable(),
		field.JSON("sources", []model.StagedSource{}).Immutable(),
		field.String("object_key").NotEmpty().Immutable(),
		field.String("filename").NotEmpty().MaxLen(255).Immutable(),
		field.String("content_type").NotEmpty().MaxLen(255).Immutable(),
		field.Int64("size").Min(0).Immutable(),
		field.String("sha256").NotEmpty().Immutable(),
		field.JSON("metrics", json.RawMessage{}).Default(json.RawMessage(`{}`)).Immutable(),
		field.Enum("state").Values("pending", "promoted", "discarded").Default("pending"),
		field.String("created_by").Immutable(),
		field.Time("updated_at").Default(func() time.Time { return time.Now().UTC() }).UpdateDefault(func() time.Time { return time.Now().UTC() }),
	}
}
func (StagedFile) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id"), index.Fields("item_id"), index.Fields("object_key", "state")}
}
