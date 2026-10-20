package dms

import (
	"bytes"
	"context"
	"encoding/json"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
	"papergo/internal/model"
	"path"
	"time"
)

type SchemaField struct {
	DefinitionID string             `json:"definition_id,omitempty"`
	Options      model.FieldOptions `json:"options,omitempty"`
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	Type         catalog.FieldType  `json:"type"`
	Required     bool               `json:"required"`
	Choices      []string           `json:"choices"`
	Indexed      bool               `json:"indexed"`
	Scale        int                `json:"scale"`
}
type SchemaDefinition struct {
	Fields       []SchemaField          `json:"fields"`
	ContentTypes []SchemaContentType    `json:"content_types"`
	Rules        []model.ValidationRule `json:"rules,omitempty"`
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
func (s *Service) recordSchema(ctx context.Context, actor string, c *resources.Resource) error {
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: c.ID})
	if err != nil {
		return err
	}
	definition := SchemaDefinition{Fields: []SchemaField{}}
	for _, d := range defs {
		definition.Fields = append(definition.Fields, schemaField(d))
	}
	types, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{CollectionID: c.ID})
	if err != nil {
		return err
	}
	definition.ContentTypes = []SchemaContentType{}
	defaultType, err := s.defaultContentType(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, typ := range types {
		definition.ContentTypes = append(definition.ContentTypes, SchemaContentType{ID: typ.ID, Key: typ.Key, Name: typ.Name, FieldKeys: fieldKeys(selectTypeDefinitions(defs, typ)), Rules: typ.Rules, IsDefault: typ.ID == defaultType})
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	if _, err = s.DAL.Catalog().RecordSchema(ctx, c.ID, actor, raw); err != nil {
		return err
	}
	return s.DAL.Resources().SchemaChanged(ctx, c.ID, actor, c.SchemaHeadID != nil)
}
func definitions(raw json.RawMessage) ([]*catalog.FieldDefinition, error) {
	var schema SchemaDefinition
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	out := make([]*catalog.FieldDefinition, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		out = append(out, &catalog.FieldDefinition{Key: f.ID, Label: f.Label, Type: f.Type, Required: f.Required, Choices: f.Choices, Indexed: f.Indexed, Scale: f.Scale, Options: f.Options})
	}
	return out, nil
}
func (s *Service) overlayHead(ctx context.Context, r *resources.Resource) error {
	if r.HeadRevisionID == nil {
		return invalid("item has no head revision")
	}
	rev, err := s.revision(ctx, *r.HeadRevisionID)
	if err != nil {
		return err
	}
	return overlayRevision(r, rev)
}
func overlayRevision(r *resources.Resource, rev *revisions.Revision) error {
	values, err := decodeValues(rev.Payload)
	if err != nil {
		return err
	}
	r.Name = rev.Name
	r.Keywords = append([]string{}, rev.Keywords...)
	r.Values = values
	r.ContentTypeID = &rev.ContentTypeID
	r.UpdatedAt = rev.CreatedAt
	r.UpdatedBy = rev.CreatedBy
	return nil
}

// revisionsByID loads revisions by id.
func (s *Service) revisionsByID(ctx context.Context, ids []string) (map[string]*revisions.Revision, error) {
	rows, err := s.DAL.Revisions().Revisions(ctx, revisions.Filter{IDs: ids})
	if err != nil {
		return nil, err
	}
	out := make(map[string]*revisions.Revision, len(rows))
	for i := range rows {
		out[rows[i].ID] = &rows[i]
	}
	return out, nil
}

// revision returns a content revision.
func (s *Service) revision(ctx context.Context, id string) (*revisions.Revision, error) {
	rev, err := s.DAL.Revisions().Revision(ctx, id)
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

func (s *Service) recordRevision(ctx context.Context, actor string, r *resources.Resource, newBlobID *string, carry bool) (*revisions.Revision, error) {
	if r.ContainerID == nil {
		return nil, invalid("item has no container")
	}
	c, err := s.batchCollection(ctx, *r.ContainerID)
	if err != nil {
		return nil, err
	}
	if c.SchemaHeadID == nil {
		return nil, invalid("container has no schema revision")
	}
	schema, err := s.schemaRevision(ctx, *c.SchemaHeadID)
	if err != nil {
		return nil, err
	}
	if r.ContentTypeID == nil {
		return nil, invalid("item has no content type")
	}
	defs, rules, err := typedDefinitions(schema.Definition, *r.ContentTypeID)
	if err != nil {
		return nil, err
	}
	if carry {
		known := definitionMap(defs)
		values := map[string]any{}
		for key, value := range r.Values {
			if known[key] != nil {
				values[key] = value
			}
		}
		r.Values = values
	}
	var previous map[string]any
	var previousRevision *revisions.Revision
	blobID := newBlobID
	if r.HeadRevisionID != nil {
		old, e := s.revision(ctx, *r.HeadRevisionID)
		if e != nil {
			return nil, e
		}
		previousRevision = old
		previous, e = decodeValues(old.Payload)
		if e != nil {
			return nil, e
		}
		if blobID == nil {
			blobID = old.BlobID
		}
	}
	applyDefaults := previous == nil
	if previousRevision != nil && previousRevision.ContentTypeID != *r.ContentTypeID {
		// Reclassification enforces the destination type and required fields;
		// creation defaults are not implicitly applied by an update.
		previous = nil
		applyDefaults = false
	}
	defs, rules = resourceDefinitions(r.Kind, defs, rules)
	if r.Kind == refs.KindFolder {
		applyDefaults = false
	}
	if err = s.normalizeItemValues(ctx, actor, defs, r.Values, previous, applyDefaults); err != nil {
		return nil, err
	}
	if err = evaluateRules(defs, rules, r.Values); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r.Values)
	if err != nil {
		return nil, err
	}
	filename := ""
	if blobID != nil {
		if newBlobID != nil {
			file, err := s.blob(ctx, *blobID)
			if err != nil {
				return nil, err
			}
			filename = file.Filename
		} else if previousRevision != nil {
			filename = previousRevision.Filename
			if r.Name != previousRevision.Name {
				filename = r.Name
				if path.Ext(filename) == "" {
					filename += path.Ext(previousRevision.Filename)
				}
			}
		}
		if !filenameValid(filename) {
			return nil, invalid("invalid attachment filename")
		}
	}
	values, err := json.Marshal(r.Values)
	if err != nil {
		return nil, err
	}
	recorded, err := s.DAL.Resources().Revise(ctx, revisions.Revision{Filename: filename, ItemID: r.ID, ContainerID: c.ID, ContentTypeID: *r.ContentTypeID,
		SchemaRevisionID: schema.ID, Name: r.Name, Keywords: r.Keywords, Payload: raw, CreatedBy: actor, BlobID: blobID}, values, actor)
	if err != nil {
		return nil, err
	}
	rev := &recorded
	if err = s.recordIntegrationData(ctx, rev, previousRevision); err != nil {
		return nil, err
	}
	r.HeadRevisionID = &rev.ID
	r.NextRevisionNumber = rev.RevisionNumber + 1
	// Without publishing, head and published content are one, kept only as the
	// published surface: written once below, indexed once.
	if c.PublishingEnabled {
		if err = s.replaceSurface(ctx, r, rev, refs.SurfaceHead, defs); err != nil {
			return nil, err
		}
	}
	if r.Kind == refs.KindFolder {
		// Folder metadata is immediately visible. Internal schema snapshots keep
		// its projections consistent without creating publications or history.
		if err = s.replaceSurface(ctx, r, rev, refs.SurfacePublished, defs); err != nil {
			return nil, err
		}
		err = s.DAL.Resources().SetPublished(ctx, r.ID, rev.ID, false)
		if err == nil {
			err = s.collectRevisions(ctx, r.ID)
		}
		typ := EventFolderUpdated
		if rev.RevisionNumber == 1 {
			typ = EventFolderCreated
		}
		s.emit(ctx, typ, actor, r.WorkspaceID, r.ContainerID, r.ID, map[string]any{"revision_id": rev.ID, "revision_number": rev.RevisionNumber, "content_type_id": rev.ContentTypeID})
		return rev, err
	}
	typ := EventItemUpdated
	if rev.RevisionNumber == 1 {
		typ = EventItemCreated
	}
	data := map[string]any{"revision_id": rev.ID, "revision_number": rev.RevisionNumber, "content_type_id": rev.ContentTypeID}
	if rev.BlobID != nil {
		data["blob_id"] = *rev.BlobID
	}
	s.emit(ctx, typ, actor, r.WorkspaceID, r.ContainerID, r.ID, data)
	if !c.PublishingEnabled {
		if _, err = s.publishRevision(ctx, actor, r, rev, false, nil); err != nil {
			return nil, err
		}
	}
	if err = s.retainHistory(ctx, c, rev, previousRevision); err != nil {
		return nil, err
	}
	if c.Versioning == resources.VersioningOff && previousRevision != nil && previousRevision.BlobID != nil && newBlobID != nil && *previousRevision.BlobID != *newBlobID {
		oldBlob, err := s.blob(ctx, *previousRevision.BlobID)
		if err != nil {
			return nil, err
		}
		if _, err = s.reclaimBlob(ctx, oldBlob); err != nil {
			return nil, err
		}
	}
	return rev, nil
}
func (s *Service) replaceSurface(ctx context.Context, r *resources.Resource, rev *revisions.Revision, surface refs.Surface, defs []*catalog.FieldDefinition) error {
	current, err := s.collectionDefinitions(ctx, *r.ContainerID)
	if err != nil {
		return err
	}
	values, err := fieldValueRows(rev.Payload, indexedDefinitions(current, defs))
	if err != nil {
		return err
	}
	if _, err := s.DAL.Surfaces().Project(ctx, surfaces.Projection{ItemID: r.ID, Surface: surface, RevisionID: rev.ID, ModifiedAt: rev.CreatedAt, ModifiedBy: rev.CreatedBy, Fields: values}); err != nil {
		return err
	}
	return s.claimBusinessKeys(ctx, r.ID, *r.ContainerID, current)
}

// indexedDefinitions copies a revision's definitions, marking a field indexed only
// while the collection still indexes it under the same identity.
func indexedDefinitions(current, defs []*catalog.FieldDefinition) []*catalog.FieldDefinition {
	configured := map[string]*catalog.FieldDefinition{}
	for _, d := range current {
		configured[d.Key] = d
	}
	out := make([]*catalog.FieldDefinition, 0, len(defs))
	for _, d := range defs {
		now := configured[d.Key]
		copy := *d
		copy.Indexed = now != nil && now.Indexed && now.Type == d.Type && now.Scale == d.Scale && optionsIdentityEqual(now.Options, d.Options)
		out = append(out, &copy)
	}
	return out
}

// indexSurface replaces the surface's field_values rows from its payload.
func (s *Service) indexSurface(ctx context.Context, projection surfaces.Surface, defs []*catalog.FieldDefinition) error {
	values, err := fieldValueRows(projection.Payload, defs)
	if err != nil {
		return err
	}
	return s.DAL.Surfaces().Reindex(ctx, projection.ID, nil, values)
}

// fieldValueRows builds the indexed field_values rows of one surface without writing them.
func fieldValueRows(payload json.RawMessage, defs []*catalog.FieldDefinition) ([]surfaces.FieldValue, error) {
	values, err := decodeValues(payload)
	if err != nil {
		return nil, err
	}
	rows := []surfaces.FieldValue{}
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
			b := surfaces.FieldValue{Ordinal: int64(ordinal), FieldKey: d.Key, FieldType: d.Type, Scale: int64(d.Scale)}
			switch string(d.Type) {
			case "text", "note", "email", "url", "date", "lookup", "person", "term", "keywords", "choice":
				value := v.(string)
				b.ValueText = &value
			case "datetime":
				date, err := time.Parse(time.RFC3339, v.(string))
				if err != nil {
					return nil, err
				}
				value := date.UTC().Format("2006-01-02T15:04:05.000000000Z")
				b.ValueText = &value
			case "integer":
				n, err := integer(v)
				if err != nil {
					return nil, err
				}
				b.ValueInteger = &n
			case "decimal":
				_, n, err := decimal(v, d.Scale)
				if err != nil {
					return nil, err
				}
				b.ValueInteger = &n
			case "number", "currency":
				n, err := numberKey(v)
				if err != nil {
					return nil, err
				}
				b.ValueText = &n
			case "boolean":
				value := v.(bool)
				b.ValueBoolean = &value
			}
			rows = append(rows, b)
		}
	}
	return rows, nil
}

// publishRevision reuses schema revisions from schemas, which may be nil, and
// adds the ones it loads.
func (s *Service) publishRevision(ctx context.Context, actor string, r *resources.Resource, rev *revisions.Revision, explicit bool, schemas map[string]*catalog.SchemaRevision) (*revisions.Publication, error) {
	if explicit && r.PublishedRevisionID != nil && *r.PublishedRevisionID == rev.ID {
		return nil, ErrConflict
	}
	schema := schemas[rev.SchemaRevisionID]
	if schema == nil {
		var err error
		if schema, err = s.schemaRevision(ctx, rev.SchemaRevisionID); err != nil {
			return nil, err
		}
		if schemas != nil {
			schemas[schema.ID] = schema
		}
	}
	defs, _, err := typedDefinitions(schema.Definition, rev.ContentTypeID)
	if err != nil {
		return nil, err
	}
	if err = s.replaceSurface(ctx, r, rev, refs.SurfacePublished, defs); err != nil {
		return nil, err
	}
	snapshot := map[string]any{"id": r.ID, "name": rev.Name, "keywords": rev.Keywords, "values": rev.Payload, "revision_id": rev.ID, "revision_number": rev.RevisionNumber, "schema_revision_id": rev.SchemaRevisionID, "filename": rev.Filename}
	if rev.BlobID != nil {
		snapshot["blob_id"] = *rev.BlobID
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	published, err := s.DAL.Revisions().Publish(ctx, revisions.Publication{ItemID: r.ID, RevisionID: &rev.ID, Version: r.Version, Action: revisions.ActionPublish, PublishedBy: actor, Snapshot: raw})
	if err != nil {
		return nil, err
	}
	event := &published
	if err = s.DAL.Resources().SetPublished(ctx, r.ID, rev.ID, explicit); err != nil {
		return nil, err
	}
	r.PublishedRevisionID = &rev.ID
	s.emit(ctx, EventItemPublished, actor, r.WorkspaceID, r.ContainerID, r.ID, map[string]any{"revision_id": rev.ID, "revision_number": rev.RevisionNumber, "publication_id": event.ID})
	return event, nil
}

// addHeadSurfaces gives every item and folder of a collection that starts
// publishing its own head surface, a copy of the published one, so drafts can
// differ from what readers see. Pages of surfaceBatch keep memory flat.
func (s *Service) addHeadSurfaces(ctx context.Context, c *resources.Resource) error {
	schemas := map[string]*catalog.SchemaRevision{}
	for after := ""; ; {
		rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{ContainerID: c.ID, Kinds: contentKinds, State: resources.Live, WithHead: true, AfterID: after, Limit: surfaceBatch})
		if err != nil {
			return err
		}
		heads := make([]string, 0, len(rows))
		for _, r := range rows {
			heads = append(heads, *r.HeadRevisionID)
		}
		byID, err := s.revisionsByID(ctx, heads)
		if err != nil {
			return err
		}
		for _, r := range rows {
			rev := byID[*r.HeadRevisionID]
			schema := schemas[rev.SchemaRevisionID]
			if schema == nil {
				if schema, err = s.schemaRevision(ctx, rev.SchemaRevisionID); err != nil {
					return err
				}
				schemas[schema.ID] = schema
			}
			defs, _, err := typedDefinitions(schema.Definition, rev.ContentTypeID)
			if err != nil {
				return err
			}
			if err = s.replaceSurface(ctx, r, rev, refs.SurfaceHead, defs); err != nil {
				return err
			}
		}
		if len(rows) < surfaceBatch {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// dropHeadSurfaces removes the head surfaces of a collection that stops
// publishing, after its heads were published: they equal the published ones.
func (s *Service) dropHeadSurfaces(ctx context.Context, collection string) error {
	return s.DAL.Surfaces().DropSurface(ctx, collection, refs.SurfaceHead)
}

// publishAllHeads publishes every unpublished head in surfaceBatch pages, so
// memory stays flat and head revisions and schemas load once per batch.
func (s *Service) publishAllHeads(ctx context.Context, actor string, c *resources.Resource) error {
	schemas := map[string]*catalog.SchemaRevision{}
	for after := ""; ; {
		rows, err := s.DAL.Resources().Resources(ctx, resources.Filter{ContainerID: c.ID, Kinds: []refs.ResourceKind{refs.KindItem}, State: resources.Live, Unpublished: true, AfterID: after, Limit: surfaceBatch})
		if err != nil {
			return err
		}
		heads := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.HeadRevisionID == nil {
				return invalid("item has no head revision")
			}
			heads = append(heads, *r.HeadRevisionID)
		}
		byID, err := s.revisionsByID(ctx, heads)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if _, err = s.publishRevision(ctx, actor, r, byID[*r.HeadRevisionID], true, schemas); err != nil {
				return err
			}
		}
		if len(rows) < surfaceBatch {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// ItemSchema exposes only the schema belonging to the caller's visible revision.

// ItemSchema pairs the visible schema with the same surface's system properties.
type ItemSchema struct {
	*catalog.SchemaRevision
	Item *resources.Resource `json:"item"`
	File *SchemaFile         `json:"file,omitempty"`
}

type SchemaFile struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

func (s *Service) ItemSchema(ctx context.Context, subject, id, surface string) (*ItemSchema, error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (*ItemSchema, error) { return t.ItemSchema(ctx, subject, id, surface) })
	}
	r, err := s.GetSurface(ctx, subject, id, surface)
	if err != nil {
		return nil, err
	}
	if !contentKind(r.Kind) {
		return nil, invalid("content schema belongs to an item or folder")
	}
	selected := r.HeadRevisionID
	if selected == nil {
		selected = r.PublishedRevisionID
	}
	if selected == nil {
		return nil, ErrNotFound
	}
	rev, err := s.revision(ctx, *selected)
	if err != nil {
		return nil, err
	}
	schema, err := s.DAL.Catalog().Schema(ctx, rev.SchemaRevisionID)
	if err != nil {
		return nil, err
	}
	out := &ItemSchema{SchemaRevision: schema, Item: r}
	if rev.BlobID != nil {
		b, err := s.blob(ctx, *rev.BlobID)
		if err != nil {
			return nil, err
		}
		out.File = &SchemaFile{Filename: rev.Filename, ContentType: string(b.ContentType), Size: b.Size}
	}
	return out, nil
}

func (s *Service) Schemas(ctx context.Context, subject, id string, after, limit int) (Page[*catalog.SchemaRevision], error) {
	if !s.transaction {
		return read(ctx, s, func(t *Service) (Page[*catalog.SchemaRevision], error) {
			return t.Schemas(ctx, subject, id, after, limit)
		})
	}
	r, err := s.authorize(ctx, subject, id, "manage")
	if err != nil {
		return Page[*catalog.SchemaRevision]{}, err
	}
	if r.Kind != refs.KindList && r.Kind != refs.KindLibrary {
		return Page[*catalog.SchemaRevision]{}, invalid("schema revisions belong to collections")
	}
	rows, err := s.DAL.Catalog().Schemas(ctx, catalog.SchemaFilter{ContainerID: id, AfterNumber: after, Limit: pageSize(limit) + 1})
	if err != nil {
		return Page[*catalog.SchemaRevision]{}, err
	}
	return numberedPage(rows, limit, func(v *catalog.SchemaRevision) int { return v.RevisionNumber }), nil
}

func schemaField(d *catalog.FieldDefinition) SchemaField {
	return SchemaField{DefinitionID: d.ID, ID: d.Key, Label: d.Label, Type: d.Type, Required: d.Required, Choices: d.Choices, Indexed: d.Indexed, Scale: d.Scale, Options: d.Options}
}
