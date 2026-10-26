package dms

import (
	"context"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/model"
	"slices"
)

// DeleteField removes a field from the list types of a collection that own
// it; the catalog types and other collections keep theirs. A field the
// collection inherits from a catalog type stays. The definition goes once no
// content type has the field.
func (s *Service) DeleteField(ctx context.Context, subject, collection, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		c, err := t.contentTypeResource(ctx, subject, collection, "manage")
		if err != nil {
			return err
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		d, err := t.DAL.Catalog().Field(ctx, id)
		if dberr.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		types, err := t.collectionTypes(ctx, c.ID)
		if err != nil {
			return err
		}
		owners := []*catalog.ContentType{}
		for _, typ := range types {
			if slices.Contains(typ.FieldIds, d.ID) {
				owners = append(owners, typ)
			} else if slices.Contains(typ.Effective.FieldIds, d.ID) {
				return invalid("field " + d.Key + " comes from content type " + typ.Name + "; hide it in this collection or remove it from the catalog type")
			}
		}
		if len(owners) == 0 {
			return ErrNotFound
		}
		for _, typ := range owners {
			if dependsOn(typ.Rules, d.Key) {
				return invalid("remove dependent content-type rules before deleting this field")
			}
		}
		defs, err := t.collectionFields(ctx, c.ID)
		if err != nil {
			return err
		}
		known := definitionMap(defs)
		delete(known, d.Key)
		if err = t.validateSchemaViews(ctx, c.ID, known); err != nil {
			return err
		}
		for _, typ := range owners {
			ids := slices.DeleteFunc(slices.Clone(typ.FieldIds), func(id string) bool { return id == d.ID })
			if _, err = t.reviseContentType(ctx, typ, catalog.ContentType{FieldIds: ids, HiddenFieldIds: typ.HiddenFieldIds, Rules: typ.Rules}); err != nil {
				return err
			}
		}
		if err = t.removeCollectionField(ctx, c.ID, d.Key); err != nil {
			return err
		}
		if err = t.refreshCollections(ctx, subject, []*resources.Resource{c}); err != nil {
			return err
		}
		held, err := t.DAL.Catalog().ContentTypeExists(ctx, catalog.ContentTypeFilter{FieldID: d.ID, Detached: true})
		if err != nil {
			return err
		}
		if !held {
			if err = t.DAL.Catalog().DeleteField(ctx, d.ID); err != nil {
				return err
			}
		}
		return t.audit(ctx, subject, "field.remove", c, map[string]any{"field_id": d.ID, "key": d.Key})
	})
}

// dependsOn reports whether a rule refers to a field key.
func dependsOn(rules []model.ValidationRule, key string) bool {
	return slices.ContainsFunc(rules, func(rule model.ValidationRule) bool {
		return rule.Field == key || rule.OtherField == key || rule.WhenField == key
	})
}

// removeCollectionField drops a removed field's projections and releases its
// key in a collection.
func (s *Service) removeCollectionField(ctx context.Context, collection, key string) error {
	if err := s.DAL.Surfaces().RemoveField(ctx, collection, key); err != nil {
		return err
	}
	return s.DAL.Catalog().ReleaseKeys(ctx, catalog.KeyFilter{CollectionID: collection, FieldKey: key})
}

func (s *Service) DeleteCatalogField(ctx context.Context, subject, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
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
		return t.deleteCatalogField(ctx, subject, d)
	})
}
func (s *Service) deleteCatalogField(ctx context.Context, subject string, d *catalog.FieldDefinition) error {
	types, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{FieldID: d.ID})
	if err != nil {
		return err
	}
	// Subtypes inherit the field and may have rules on it.
	owners := make([]string, len(types))
	for i, typ := range types {
		owners[i] = typ.ID
	}
	subtree, err := s.DAL.Catalog().Descendants(ctx, owners)
	if err != nil {
		return err
	}
	dependents, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{IDs: subtree, Detached: true})
	if err != nil {
		return err
	}
	for _, typ := range dependents {
		if dependsOn(typ.Rules, d.Key) {
			return invalid("remove dependent content-type rules before deleting this field")
		}
	}
	collections, err := s.fieldCollections(ctx, d.ID)
	if err != nil {
		return err
	}
	for _, c := range collections {
		defs, e := s.collectionFields(ctx, c.ID)
		if e != nil {
			return e
		}
		known := definitionMap(defs)
		delete(known, d.Key)
		if e = s.validateSchemaViews(ctx, c.ID, known); e != nil {
			return e
		}
	}
	for _, typ := range types {
		if slices.Contains(typ.FieldIds, d.ID) {
			ids := slices.DeleteFunc(slices.Clone(typ.FieldIds), func(id string) bool { return id == d.ID })
			if _, err := s.reviseContentType(ctx, typ, catalog.ContentType{FieldIds: ids, HiddenFieldIds: typ.HiddenFieldIds, Rules: typ.Rules}); err != nil {
				return err
			}
		}
	}
	if err := s.DAL.Catalog().DeleteField(ctx, d.ID); err != nil {
		return err
	}
	for _, c := range collections {
		if e := s.removeCollectionField(ctx, c.ID, d.Key); e != nil {
			return e
		}
	}
	if err = s.refreshCollections(ctx, subject, collections); err != nil {
		return err
	}
	return s.auditCatalog(ctx, subject, "field.delete", map[string]any{"field_id": d.ID, "key": d.Key})
}
