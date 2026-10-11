package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"papergo/ent/schemarevision"
	"papergo/internal/model"
)

// Stable keys, types and decimal scales are immutable. A new type uses a new key;
// labels, choices, requirements and query indexing evolve via schema revisions.
type UpdateField struct {
	Options  *model.FieldOptions `json:"options,omitempty"`
	Label    *string             `json:"label,omitempty"`
	Required *bool               `json:"required,omitempty"`
	Choices  *[]string           `json:"choices,omitempty"`
	Indexed  *bool               `json:"indexed,omitempty"`
}

func (s *Service) UpdateField(ctx context.Context, subject, containerID, fieldID string, version int, in UpdateField) (out *ent.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		d, e := t.Client.FieldDefinition.Query().Where(collectionFieldPredicate(c.ID), fielddefinition.IDEQ(fieldID)).Only(ctx)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		out, e = t.updateCatalogField(ctx, subject, d, in)
		return e
	})
	return
}
func (s *Service) UpdateCatalogField(ctx context.Context, subject, id string, version int, in UpdateField) (out *ent.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		d, e := t.Client.FieldDefinition.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if d.Version != version {
			return ErrPreconditionFailed
		}
		out, e = t.updateCatalogField(ctx, subject, d, in)
		return e
	})
	return
}
func (s *Service) updateCatalogField(ctx context.Context, subject string, d *ent.FieldDefinition, in UpdateField) (out *ent.FieldDefinition, err error) {
	if in.Label == nil && in.Required == nil && in.Choices == nil && in.Indexed == nil && in.Options == nil {
		return nil, invalid("at least one field change is required")
	}
	if in.Label != nil {
		if err = validateName(*in.Label); err != nil {
			return
		}
	}
	if in.Choices != nil {
		if len(*in.Choices) == 0 {
			return nil, invalid("choice field requires choices")
		}
		if err = validateTags(*in.Choices); err != nil {
			return
		}
	}

	var e error
	if in.Choices != nil && d.Type != fielddefinition.TypeChoice {
		return nil, invalid("choices belong to choice fields")
	}
	candidate := *d
	if in.Label != nil {
		candidate.Label = *in.Label
	}
	if in.Required != nil {
		candidate.Required = *in.Required
	}
	if in.Indexed != nil {
		candidate.Indexed = *in.Indexed
	}
	if in.Choices != nil {
		candidate.Choices = *in.Choices
	}
	if in.Options != nil {
		candidate.Options = *in.Options
	}
	if !optionsIdentityEqual(d.Options, candidate.Options) {
		return nil, invalid("cardinality and reference scope are immutable")
	}
	if e = validateFieldDefinition(&candidate); e != nil {
		return nil, e
	}
	if e = s.validateReferenceScopes(ctx, subject, &candidate); e != nil {
		return nil, e
	}
	if len(candidate.Options.DefaultValue) > 0 {
		if e = s.normalizeItemValues(ctx, subject, []*ent.FieldDefinition{&candidate}, map[string]any{}, nil, true); e != nil {
			return nil, e
		}
	}
	if newlyRequired(d, &candidate) {
		collections, e := s.fieldCollections(ctx, d.ID)
		if e != nil {
			return nil, e
		}
		for _, c := range collections {
			if e = s.checkRequiredFilled(ctx, c.ID, []string{d.Key}); e != nil {
				return nil, e
			}
		}
	}
	reindex := in.Indexed != nil && *in.Indexed != d.Indexed
	b := s.Client.FieldDefinition.UpdateOne(d).AddVersion(1).SetOptions(candidate.Options)
	if in.Label != nil {
		b.SetLabel(*in.Label)
	}
	if in.Required != nil {
		b.SetRequired(*in.Required)
	}
	if in.Choices != nil {
		b.SetChoices(*in.Choices)
	}
	if in.Indexed != nil {
		b.SetIndexed(*in.Indexed)
	}
	out, e = b.Save(ctx)
	if e != nil {
		return nil, e
	}
	if e = s.bumpFieldTypes(ctx, d.ID); e != nil {
		return nil, e
	}
	collections, e := s.fieldCollections(ctx, d.ID)
	if e != nil {
		return nil, e
	}
	if e = s.refreshCollections(ctx, subject, collections); e != nil {
		return nil, e
	}
	if reindex {
		if e = s.enqueueFieldIndex(ctx, subject, out); e != nil {
			return nil, e
		}
		if out, e = s.Client.FieldDefinition.Get(ctx, d.ID); e != nil {
			return nil, e
		}
	}

	err = s.auditCatalog(ctx, subject, "field.update", map[string]any{"field_id": d.ID, "key": d.Key, "version": out.Version})
	return
}

// newlyRequired reports whether a change makes a field required without a default,
// which, like a new required field, needs every existing item to hold a value.
func newlyRequired(old, d *ent.FieldDefinition) bool {
	return d.Required && !old.Required && len(d.Options.DefaultValue) == 0
}

// checkRequiredFilled rejects making fields required while item heads lack values.
func (s *Service) checkRequiredFilled(ctx context.Context, containerID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	types, err := s.Client.ContentType.Query().Where(collectionTypePredicate(containerID)).All(ctx)
	if err != nil {
		return err
	}
	defs, err := s.collectionFields(ctx, containerID)
	if err != nil {
		return err
	}
	membership := map[string]map[string]bool{}
	for _, typ := range types {
		membership[typ.ID] = map[string]bool{}
		for _, key := range fieldKeys(selectTypeDefinitions(defs, typ)) {
			membership[typ.ID][key] = true
		}
	}
	for after := ""; ; {
		heads, e := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(containerID), itemsurface.SurfaceEQ(itemsurface.SurfaceHead), itemsurface.ItemIDGT(after)).Order(ent.Asc(itemsurface.FieldItemID)).Limit(surfaceBatch).Select(itemsurface.FieldItemID, itemsurface.FieldPayload).All(ctx)
		if e != nil {
			return e
		}
		ids := []string{}
		for _, head := range heads {
			ids = append(ids, head.ItemID)
		}
		items, e := s.Client.Resource.Query().Where(resource.IDIn(ids...)).Select(resource.FieldID, resource.FieldContentTypeID).All(ctx)
		if e != nil {
			return e
		}
		itemTypes := map[string]string{}
		for _, item := range items {
			if item.ContentTypeID != nil {
				itemTypes[item.ID] = *item.ContentTypeID
			}
		}
		for _, h := range heads {
			values, e := decodeValues(h.Payload)
			if e != nil {
				return e
			}
			for _, key := range keys {
				if !membership[itemTypes[h.ItemID]][key] {
					continue
				}
				if emptyValue(values[key]) {
					return invalid("required fields on populated collections need a default or a value on every item: " + key)
				}
			}
		}
		if len(heads) < surfaceBatch {
			return nil
		}
		after = heads[len(heads)-1].ItemID
	}
}

// surfaceBatch bounds whole-collection passes: memory stays flat, IN lists
// stay far below SQLite's bound-variable limit (32766) however large the collection.
var surfaceBatch = 500

// reindexBatch replaces field d's field_values on a batch of surfaces with one
// delete and a few bulk inserts; an unindexed d just loses its rows. indexed
// caches d's per-schema-revision definitions across the batch.
func (s *Service) reindexBatch(ctx context.Context, projections []*ent.ItemSurface, d *ent.FieldDefinition, indexed map[string][]*ent.FieldDefinition) error {
	current := []*ent.FieldDefinition{d}
	revisionIDs := make([]string, 0, len(projections))
	for _, p := range projections {
		revisionIDs = append(revisionIDs, p.RevisionID)
	}
	revisions, e := s.Client.ItemRevision.Query().Where(itemrevision.IDIn(revisionIDs...)).Select(itemrevision.FieldID, itemrevision.FieldSchemaRevisionID).All(ctx)
	if e != nil {
		return e
	}
	schemaOf := map[string]string{}
	missing := map[string]bool{}
	for _, rev := range revisions {
		schemaOf[rev.ID] = rev.SchemaRevisionID
		if _, ok := indexed[rev.SchemaRevisionID]; !ok {
			missing[rev.SchemaRevisionID] = true
		}
	}
	if len(missing) > 0 {
		schemaIDs := make([]string, 0, len(missing))
		for id := range missing {
			schemaIDs = append(schemaIDs, id)
		}
		schemas, e := s.Client.SchemaRevision.Query().Where(schemarevision.IDIn(schemaIDs...)).All(ctx)
		if e != nil {
			return e
		}
		for _, schema := range schemas {
			defs, e := definitions(schema.Definition)
			if e != nil {
				return e
			}
			indexed[schema.ID] = indexedDefinitions(current, defs)
		}
	}
	surfaceIDs := make([]string, 0, len(projections))
	rows := []*ent.FieldValueCreate{}
	for _, p := range projections {
		defs, ok := indexed[schemaOf[p.RevisionID]]
		if !ok {
			return ErrNotFound
		}
		built, e := s.fieldValueRows(p, defs)
		if e != nil {
			return e
		}
		surfaceIDs = append(surfaceIDs, p.ID)
		rows = append(rows, built...)
	}
	if _, e = s.Client.FieldValue.Delete().Where(fieldvalue.SurfaceIDIn(surfaceIDs...), fieldvalue.FieldKeyEQ(d.Key)).Exec(ctx); e != nil {
		return e
	}
	return s.insertFieldValues(ctx, rows)
}
