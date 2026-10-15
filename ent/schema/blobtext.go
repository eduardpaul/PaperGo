package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BlobText is the text read from the file bytes with a SHA-256, once per
// set of reading settings: identical files share it. The page texts are a
// JSON object in the blob store, which the row pins; it lives while a file
// with those bytes does.
type BlobText struct{ ent.Schema }

func (BlobText) Mixin() []ent.Mixin { return []ent.Mixin{Identity{}} }
func (BlobText) Fields() []ent.Field {
	return []ent.Field{
		field.String("sha256").NotEmpty().Immutable(),
		// Settings identify how the text was read: reader, OCR mode and
		// engine, page bound.
		field.String("settings").NotEmpty().Immutable(),
		field.String("object_key").NotEmpty().Immutable(),
		field.Int64("object_size").Min(0).Immutable(),
		field.String("object_sha256").NotEmpty().Immutable(),
		field.Int("pages").Min(0).Immutable(),
		field.Int("chars").Min(0).Immutable(),
		field.Int("ocr_pages").Min(0).Immutable(),
		field.Int("skipped_pages").Min(0).Immutable(),
		field.Bool("truncated").Immutable(),
		field.String("language").Default("").Immutable(),
		field.String("extractor").NotEmpty().Immutable(),
		field.JSON("models", json.RawMessage{}).Default(json.RawMessage(`[]`)).Immutable(),
	}
}
func (BlobText) Indexes() []ent.Index {
	return []ent.Index{index.Fields("sha256", "settings").Unique(), index.Fields("object_key")}
}
