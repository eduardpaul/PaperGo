package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/fieldvalue"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
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
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrConflict
		}
		d, e := t.Client.FieldDefinition.Get(ctx, fieldID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if d.ContainerID != containerID {
			return ErrNotFound
		}
		if in.Choices != nil && d.Type != fielddefinition.TypeChoice {
			return invalid("choices belong to choice fields")
		}
		candidate := *d
		if in.Label != nil {
			candidate.Label = *in.Label
		}
		if in.Required != nil {
			candidate.Required = *in.Required
		}
		if in.Choices != nil {
			candidate.Choices = *in.Choices
		}
		if in.Options != nil {
			candidate.Options = *in.Options
		}
		if !optionsIdentityEqual(d.Options, candidate.Options) {
			return invalid("cardinality and reference scope are immutable")
		}
		if e = validateFieldDefinition(&candidate); e != nil {
			return e
		}
		if e = t.validateReferenceScopes(ctx, subject, c.WorkspaceID, &candidate); e != nil {
			return e
		}
		if len(candidate.Options.DefaultValue) > 0 {
			if e = t.normalizeItemValues(ctx, subject, c.WorkspaceID, []*ent.FieldDefinition{&candidate}, map[string]any{}, nil); e != nil {
				return e
			}
		}
		if newlyRequired(d, &candidate) {
			if e = t.checkRequiredFilled(ctx, containerID, []string{d.Key}); e != nil {
				return e
			}
		}
		reindex := in.Indexed != nil && *in.Indexed != d.Indexed
		b := t.Client.FieldDefinition.UpdateOne(d).SetOptions(candidate.Options)
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
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		if reindex {
			if e = t.rebuildSurfaces(ctx, containerID); e != nil {
				return e
			}
		}
		return t.audit(ctx, subject, "field.update", c, map[string]any{"field_id": fieldID, "key": d.Key})
	})
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
	for after := ""; ; {
		heads, e := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(containerID), itemsurface.SurfaceEQ(itemsurface.SurfaceHead), itemsurface.ItemIDGT(after)).Order(ent.Asc(itemsurface.FieldItemID)).Limit(surfaceBatch).Select(itemsurface.FieldItemID, itemsurface.FieldPayload).All(ctx)
		if e != nil {
			return e
		}
		for _, h := range heads {
			values, e := decodeValues(h.Payload)
			if e != nil {
				return e
			}
			for _, key := range keys {
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

// surfaceBatch bounds whole-collection passes: memory stays flat and IN lists
// stay far below SQLite's bound-variable limit (32766) however large the collection.
var surfaceBatch = 500

// rebuildSurfaces re-derives the field_values index of every surface in the
// collection. Only index membership changes need it; surface rows are untouched.
func (s *Service) rebuildSurfaces(ctx context.Context, containerID string) error {
	current, e := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(containerID)).All(ctx)
	if e != nil {
		return e
	}
	// Collections share few schema revisions, so their definitions are cached across batches.
	indexed := map[string][]*ent.FieldDefinition{}
	// Paging each surface by item_id walks the (container_id, surface, item_id)
	// index; any other order makes every batch rescan the whole collection.
	for _, surface := range []itemsurface.Surface{itemsurface.SurfaceHead, itemsurface.SurfacePublished} {
		for after := ""; ; {
			projections, e := s.Client.ItemSurface.Query().Where(itemsurface.ContainerIDEQ(containerID), itemsurface.SurfaceEQ(surface), itemsurface.ItemIDGT(after)).Order(ent.Asc(itemsurface.FieldItemID)).Limit(surfaceBatch).All(ctx)
			if e != nil {
				return e
			}
			if len(projections) > 0 {
				if e = s.reindexBatch(ctx, projections, current, indexed); e != nil {
					return e
				}
			}
			if len(projections) < surfaceBatch {
				break
			}
			after = projections[len(projections)-1].ItemID
		}
	}
	return nil
}

// reindexBatch replaces the field_values of a batch of surfaces with one delete
// and a few bulk inserts, rather than two statements per surface.
func (s *Service) reindexBatch(ctx context.Context, projections []*ent.ItemSurface, current []*ent.FieldDefinition, indexed map[string][]*ent.FieldDefinition) error {
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
	if _, e = s.Client.FieldValue.Delete().Where(fieldvalue.SurfaceIDIn(surfaceIDs...)).Exec(ctx); e != nil {
		return e
	}
	return s.insertFieldValues(ctx, rows)
}
