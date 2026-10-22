package dms

import (
	"context"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
	"papergo/internal/dal/surfaces"
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

func (s *Service) UpdateField(ctx context.Context, subject, containerID, fieldID string, version int, in UpdateField) (out *catalog.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		d, e := t.DAL.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: c.ID, IDs: []string{fieldID}})
		if dberr.IsNotFound(e) {
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
func (s *Service) UpdateCatalogField(ctx context.Context, subject, id string, version int, in UpdateField) (out *catalog.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		d, e := t.DAL.Catalog().Field(ctx, id)
		if dberr.IsNotFound(e) {
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
func (s *Service) updateCatalogField(ctx context.Context, subject string, d *catalog.FieldDefinition, in UpdateField) (out *catalog.FieldDefinition, err error) {
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
		if err = validateChoices(*in.Choices); err != nil {
			return
		}
	}

	var e error
	if in.Choices != nil && d.Type != catalog.FieldChoice {
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
		return nil, invalid("cardinality, reference scope and apply_parents are immutable")
	}
	if e = validateFieldDefinition(&candidate); e != nil {
		return nil, e
	}
	if e = s.validateReferenceScopes(ctx, subject, &candidate); e != nil {
		return nil, e
	}
	if len(candidate.Options.DefaultValue) > 0 {
		if e = s.normalizeKeywordDefault(ctx, subject, &candidate); e != nil {
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
	out, e = s.reviseFieldDefinition(ctx, d, &candidate)
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
		if out, e = s.DAL.Catalog().Field(ctx, d.ID); e != nil {
			return nil, e
		}
	}

	err = s.auditCatalog(ctx, subject, "field.update", map[string]any{"field_id": d.ID, "key": d.Key, "version": out.Version})
	return
}

// newlyRequired reports whether a change makes a field required without a default,
// which, like a new required field, needs every existing item to hold a value.
func newlyRequired(old, d *catalog.FieldDefinition) bool {
	return d.Required && !old.Required && len(d.Options.DefaultValue) == 0
}

// checkRequiredFilled rejects making fields required while item heads lack values.
func (s *Service) checkRequiredFilled(ctx context.Context, containerID string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	types, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{CollectionID: containerID})
	if err != nil {
		return err
	}
	defs, err := s.collectionFields(ctx, containerID)
	if err != nil {
		return err
	}
	c, err := s.DAL.Resources().Resource(ctx, containerID)
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
		heads, e := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ContainerID: containerID, Surface: headSurface(c), AfterItemID: after, Limit: surfaceBatch})
		if e != nil {
			return e
		}
		ids := []string{}
		for _, head := range heads {
			ids = append(ids, head.ItemID)
		}
		items, e := s.DAL.Resources().Resources(ctx, resources.Filter{IDs: ids})
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
func (s *Service) reindexBatch(ctx context.Context, projections []surfaces.Surface, d *catalog.FieldDefinition, indexed map[string][]*catalog.FieldDefinition) error {
	current := []*catalog.FieldDefinition{d}
	revisionIDs := make([]string, 0, len(projections))
	for _, p := range projections {
		revisionIDs = append(revisionIDs, p.RevisionID)
	}
	revs, e := s.DAL.Revisions().Revisions(ctx, revisions.Filter{IDs: revisionIDs})
	if e != nil {
		return e
	}
	schemaOf := map[string]string{}
	missing := map[string]bool{}
	for _, rev := range revs {
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
		schemas, e := s.DAL.Catalog().Schemas(ctx, catalog.SchemaFilter{IDs: schemaIDs})
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
	for _, p := range projections {
		defs, ok := indexed[schemaOf[p.RevisionID]]
		if !ok {
			return ErrNotFound
		}
		built, err := fieldValueRows(p.Payload, defs)
		if err != nil {
			return err
		}
		if err := s.DAL.Surfaces().Reindex(ctx, p.ID, []string{d.Key}, built); err != nil {
			return err
		}
	}
	return nil
}
