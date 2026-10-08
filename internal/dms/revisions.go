package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/publication"
	"papergo/ent/resource"
	"papergo/ent/schemarevision"
	"papergo/internal/model"
	"time"
)

type SchemaField struct {
	Options  model.FieldOptions `json:"options,omitempty"`
	ID       string             `json:"id"`
	Label    string             `json:"label"`
	Type     string             `json:"type"`
	Required bool               `json:"required"`
	Choices  []string           `json:"choices"`
	Indexed  bool               `json:"indexed"`
	Scale    int                `json:"scale"`
}
type SchemaDefinition struct {
	Fields []SchemaField `json:"fields"`
}

func decodeValues(raw json.RawMessage) (map[string]any, error) {
	var values map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&values)
	if values == nil {
		values = map[string]any{}
	}
	return values, err
}
func (s *Service) recordSchema(ctx context.Context, actor string, c *ent.Resource) error {
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(c.ID)).Order(ent.Asc(fielddefinition.FieldKey)).All(ctx)
	if err != nil {
		return err
	}
	definition := SchemaDefinition{Fields: []SchemaField{}}
	for _, d := range defs {
		definition.Fields = append(definition.Fields, schemaField(d))
	}
	number := 1
	if c.SchemaHeadID != nil {
		prev, err := s.Client.SchemaRevision.Get(ctx, *c.SchemaHeadID)
		if err != nil {
			return err
		}
		number = prev.RevisionNumber + 1
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	rev, err := s.Client.SchemaRevision.Create().SetContainerID(c.ID).SetRevisionNumber(number).SetDefinition(raw).SetCreatedBy(actor).Save(ctx)
	if err != nil {
		return err
	}
	b := s.Client.Resource.UpdateOneID(c.ID).SetSchemaHeadID(rev.ID)
	if c.SchemaHeadID != nil {
		b.AddVersion(1)
	}
	return b.Exec(ctx)
}
func definitions(raw json.RawMessage) ([]*ent.FieldDefinition, error) {
	var schema SchemaDefinition
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	out := make([]*ent.FieldDefinition, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		out = append(out, &ent.FieldDefinition{Key: f.ID, Label: f.Label, Type: fielddefinition.Type(f.Type), Required: f.Required, Choices: f.Choices, Indexed: f.Indexed, Scale: f.Scale, Options: f.Options})
	}
	return out, nil
}
func (s *Service) overlayHead(ctx context.Context, r *ent.Resource) error {
	if r.HeadRevisionID == nil {
		return invalid("item has no head revision")
	}
	rev, err := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
	if err != nil {
		return err
	}
	return overlayRevision(r, rev)
}
func overlayRevision(r *ent.Resource, rev *ent.ItemRevision) error {
	values, err := decodeValues(rev.Payload)
	if err != nil {
		return err
	}
	r.Name = rev.Name
	r.Tags = append([]string{}, rev.Tags...)
	r.Values = values
	r.UpdatedAt = rev.CreatedAt
	return nil
}
func (s *Service) recordRevision(ctx context.Context, actor string, r *ent.Resource, newBlobID *string) (*ent.ItemRevision, error) {
	if r.ContainerID == nil {
		return nil, invalid("item has no container")
	}
	c, err := s.Client.Resource.Get(ctx, *r.ContainerID)
	if err != nil {
		return nil, err
	}
	if c.SchemaHeadID == nil {
		return nil, invalid("container has no schema revision")
	}
	schema, err := s.Client.SchemaRevision.Get(ctx, *c.SchemaHeadID)
	if err != nil {
		return nil, err
	}
	defs, err := definitions(schema.Definition)
	if err != nil {
		return nil, err
	}
	var previous map[string]any
	blobID := newBlobID
	if r.HeadRevisionID != nil {
		old, e := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
		if e != nil {
			return nil, e
		}
		previous, e = decodeValues(old.Payload)
		if e != nil {
			return nil, e
		}
		if blobID == nil {
			blobID = old.BlobID
		}
	}
	if err = s.normalizeItemValues(ctx, actor, r.WorkspaceID, defs, r.Values, previous); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r.Values)
	if err != nil {
		return nil, err
	}
	rev, err := s.Client.ItemRevision.Create().SetItemID(r.ID).SetContainerID(c.ID).SetSchemaRevisionID(schema.ID).SetRevisionNumber(r.NextRevisionNumber).SetName(r.Name).SetTags(r.Tags).SetPayload(raw).SetCreatedBy(actor).SetNillableBlobID(blobID).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.Client.Resource.UpdateOneID(r.ID).SetHeadRevisionID(rev.ID).SetValues(r.Values).AddNextRevisionNumber(1).Exec(ctx); err != nil {
		return nil, err
	}
	r.HeadRevisionID = &rev.ID
	r.NextRevisionNumber++
	if err = s.replaceSurface(ctx, r, rev, itemsurface.SurfaceHead, defs); err != nil {
		return nil, err
	}
	if !c.PublishingEnabled {
		if _, err = s.publishRevision(ctx, actor, r, rev, false); err != nil {
			return nil, err
		}
	}
	return rev, nil
}
func (s *Service) replaceSurface(ctx context.Context, r *ent.Resource, rev *ent.ItemRevision, surface itemsurface.Surface, defs []*ent.FieldDefinition) error {
	current, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(*r.ContainerID)).All(ctx)
	if err != nil {
		return err
	}
	existing, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(r.ID), itemsurface.SurfaceEQ(surface)).Only(ctx)
	var projection *ent.ItemSurface
	if ent.IsNotFound(err) {
		projection, err = s.Client.ItemSurface.Create().SetItemID(r.ID).SetContainerID(*r.ContainerID).SetWorkspaceID(r.WorkspaceID).SetSurface(surface).SetRevisionID(rev.ID).SetName(rev.Name).SetTags(rev.Tags).SetPayload(rev.Payload).Save(ctx)
	} else if err == nil {
		projection, err = s.Client.ItemSurface.UpdateOne(existing).SetRevisionID(rev.ID).SetName(rev.Name).SetTags(rev.Tags).SetPayload(rev.Payload).Save(ctx)
	}
	if err != nil {
		return err
	}
	return s.indexSurface(ctx, projection, indexedDefinitions(current, defs))
}

// indexedDefinitions copies a revision's definitions, marking a field indexed only
// while the collection still indexes it under the same identity.
func indexedDefinitions(current, defs []*ent.FieldDefinition) []*ent.FieldDefinition {
	configured := map[string]*ent.FieldDefinition{}
	for _, d := range current {
		configured[d.Key] = d
	}
	out := make([]*ent.FieldDefinition, 0, len(defs))
	for _, d := range defs {
		now := configured[d.Key]
		copy := *d
		copy.Indexed = now != nil && now.Indexed && now.Type == d.Type && now.Scale == d.Scale && optionsIdentityEqual(now.Options, d.Options)
		out = append(out, &copy)
	}
	return out
}

// indexSurface replaces the surface's field_values rows from its payload.
func (s *Service) indexSurface(ctx context.Context, projection *ent.ItemSurface, defs []*ent.FieldDefinition) error {
	if _, err := s.Client.FieldValue.Delete().Where(fieldvalue.SurfaceIDEQ(projection.ID)).Exec(ctx); err != nil {
		return err
	}
	rows, err := s.fieldValueRows(projection, defs)
	if err != nil {
		return err
	}
	return s.insertFieldValues(ctx, rows)
}

// fieldValueRows builds the indexed field_values rows of one surface without writing them.
func (s *Service) fieldValueRows(projection *ent.ItemSurface, defs []*ent.FieldDefinition) ([]*ent.FieldValueCreate, error) {
	values, err := decodeValues(projection.Payload)
	if err != nil {
		return nil, err
	}
	rows := []*ent.FieldValueCreate{}
	for _, d := range defs {
		v := values[d.Key]
		if !d.Indexed || v == nil {
			continue
		}
		list := []any{v}
		if d.Options.Multiple {
			list = v.([]any)
		}
		for ordinal, v := range list {
			b := s.Client.FieldValue.Create().SetOrdinal(ordinal).SetSurfaceID(projection.ID).SetContainerID(projection.ContainerID).SetItemID(projection.ItemID).SetSurface(fieldvalue.Surface(projection.Surface)).SetFieldKey(d.Key).SetFieldType(fieldvalue.FieldType(d.Type)).SetScale(d.Scale)
			switch string(d.Type) {
			case "text", "note", "email", "url", "date", "lookup", "term", "choice":
				b.SetValueText(v.(string))
			case "datetime":
				date, err := time.Parse(time.RFC3339, v.(string))
				if err != nil {
					return nil, err
				}
				b.SetValueText(date.UTC().Format("2006-01-02T15:04:05.000000000Z"))
			case "integer":
				n, err := integer(v)
				if err != nil {
					return nil, err
				}
				b.SetValueInteger(n)
			case "decimal":
				_, n, err := decimal(v, d.Scale)
				if err != nil {
					return nil, err
				}
				b.SetValueInteger(n)
			case "number":
				n, err := number(v)
				if err != nil {
					return nil, err
				}
				b.SetValueNumber(n)
			case "boolean":
				b.SetValueBoolean(v.(bool))
			}
			rows = append(rows, b)
		}
	}
	return rows, nil
}

// insertFieldValues is chunked to stay well under SQLite's bound-parameter limit.
func (s *Service) insertFieldValues(ctx context.Context, rows []*ent.FieldValueCreate) error {
	for len(rows) > 0 {
		n := min(len(rows), 500)
		if err := s.Client.FieldValue.CreateBulk(rows[:n]...).Exec(ctx); err != nil {
			return err
		}
		rows = rows[n:]
	}
	return nil
}
func (s *Service) publishRevision(ctx context.Context, actor string, r *ent.Resource, rev *ent.ItemRevision, explicit bool) (*ent.Publication, error) {
	if explicit && r.PublishedRevisionID != nil && *r.PublishedRevisionID == rev.ID {
		return nil, ErrConflict
	}
	schema, err := s.Client.SchemaRevision.Get(ctx, rev.SchemaRevisionID)
	if err != nil {
		return nil, err
	}
	defs, err := definitions(schema.Definition)
	if err != nil {
		return nil, err
	}
	if err = s.replaceSurface(ctx, r, rev, itemsurface.SurfacePublished, defs); err != nil {
		return nil, err
	}
	snapshot := map[string]any{"id": r.ID, "name": rev.Name, "tags": rev.Tags, "values": rev.Payload, "revision_id": rev.ID, "revision_number": rev.RevisionNumber, "schema_revision_id": rev.SchemaRevisionID}
	if rev.BlobID != nil {
		snapshot["blob_id"] = *rev.BlobID
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	event, err := s.Client.Publication.Create().SetItemID(r.ID).SetRevisionID(rev.ID).SetVersion(r.Version).SetAction(publication.ActionPublish).SetPublishedBy(actor).SetSnapshot(raw).Save(ctx)
	if err != nil {
		return nil, err
	}
	b := s.Client.Resource.UpdateOneID(r.ID).SetPublishedRevisionID(rev.ID)
	if explicit {
		b.AddVersion(1)
	}
	if err = b.Exec(ctx); err != nil {
		return nil, err
	}
	r.PublishedRevisionID = &rev.ID
	return event, nil
}
func (s *Service) publishAllHeads(ctx context.Context, actor string, c *ent.Resource) error {
	rows, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.KindEQ(resource.KindItem), resource.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.HeadRevisionID == nil {
			return invalid("item has no head revision")
		}
		if r.PublishedRevisionID != nil && *r.PublishedRevisionID == *r.HeadRevisionID {
			continue
		}
		rev, err := s.Client.ItemRevision.Get(ctx, *r.HeadRevisionID)
		if err != nil {
			return err
		}
		if _, err = s.publishRevision(ctx, actor, r, rev, true); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) Revisions(ctx context.Context, subject, id string, after, limit int) ([]*ent.ItemRevision, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.ItemRevision, error) { return t.Revisions(ctx, subject, id, after, limit) })
	}
	r, err := s.authorize(ctx, subject, id, "read_draft")
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindItem {
		return nil, invalid("revisions belong to items")
	}
	return s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id), itemrevision.RevisionNumberGT(after)).WithSchemaRevision().Order(ent.Asc(itemrevision.FieldRevisionNumber)).Limit(pageSize(limit)).All(ctx)
}

// ItemSchema exposes only the schema belonging to the caller's visible revision.
func (s *Service) ItemSchema(ctx context.Context, subject, id, surface string) (*ent.SchemaRevision, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ent.SchemaRevision, error) { return t.ItemSchema(ctx, subject, id, surface) })
	}
	r, err := s.GetSurface(ctx, subject, id, surface)
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindItem {
		return nil, invalid("item schema belongs to an item")
	}
	selected := r.HeadRevisionID
	if selected == nil {
		selected = r.PublishedRevisionID
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	rev, err := s.Client.ItemRevision.Get(ctx, *selected)
	if err != nil {
		return nil, err
	}
	return s.Client.SchemaRevision.Get(ctx, rev.SchemaRevisionID)
}

func (s *Service) Schemas(ctx context.Context, subject, id string, after, limit int) ([]*ent.SchemaRevision, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) ([]*ent.SchemaRevision, error) { return t.Schemas(ctx, subject, id, after, limit) })
	}
	r, err := s.authorize(ctx, subject, id, "manage")
	if err != nil {
		return nil, err
	}
	if r.Kind != resource.KindList && r.Kind != resource.KindLibrary {
		return nil, invalid("schema revisions belong to collections")
	}
	return s.Client.SchemaRevision.Query().Where(schemarevision.ContainerIDEQ(id), schemarevision.RevisionNumberGT(after)).Order(ent.Asc(schemarevision.FieldRevisionNumber)).Limit(pageSize(limit)).All(ctx)
}

func schemaField(d *ent.FieldDefinition) SchemaField {
	return SchemaField{ID: d.Key, Label: d.Label, Type: string(d.Type), Required: d.Required, Choices: d.Choices, Indexed: d.Indexed, Scale: d.Scale, Options: d.Options}
}
