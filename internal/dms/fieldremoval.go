package dms

import (
	"context"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"slices"
)

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
		if found, e := t.DAL.Catalog().FieldExists(ctx, catalog.FieldFilter{CollectionID: c.ID, IDs: []string{d.ID}}); e != nil {
			return e
		} else if !found {
			return ErrNotFound
		}
		return t.deleteCatalogField(ctx, subject, d)
	})
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
		for _, rule := range typ.Rules {
			if rule.Field == d.Key || rule.OtherField == d.Key || rule.WhenField == d.Key {
				return invalid("remove dependent content-type rules before deleting this field")
			}
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
		if e := s.DAL.Surfaces().RemoveField(ctx, c.ID, d.Key); e != nil {
			return e
		}
		if e := s.DAL.Catalog().ReleaseKeys(ctx, catalog.KeyFilter{CollectionID: c.ID, FieldKey: d.Key}); e != nil {
			return e
		}
	}
	if err = s.refreshCollections(ctx, subject, collections); err != nil {
		return err
	}
	return s.auditCatalog(ctx, subject, "field.delete", map[string]any{"field_id": d.ID, "key": d.Key})
}
