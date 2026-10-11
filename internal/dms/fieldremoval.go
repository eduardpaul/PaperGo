package dms

import (
	"context"
	"papergo/ent"
	"papergo/ent/businesskey"
	"papergo/ent/fielddefinition"
	"papergo/ent/fieldvalue"
	"slices"
)

func filterUsesField(f *FilterExpr, key string) bool {
	if f == nil {
		return false
	}
	if f.Field == key {
		return true
	}
	if filterUsesField(f.Not, key) {
		return true
	}
	for _, child := range append(append([]FilterExpr{}, f.And...), f.Or...) {
		if filterUsesField(&child, key) {
			return true
		}
	}
	return false
}

func (s *Service) DeleteField(ctx context.Context, subject, collection, id string, version int) error {
	return s.write(ctx, func(t *Service) error {
		c, err := t.contentTypeResource(ctx, subject, collection, "manage")
		if err != nil {
			return err
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		d, err := t.Client.FieldDefinition.Get(ctx, id)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if found, e := t.Client.FieldDefinition.Query().Where(collectionFieldPredicate(c.ID), fielddefinition.IDEQ(d.ID)).Exist(ctx); e != nil {
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
		return t.deleteCatalogField(ctx, subject, d)
	})
}
func (s *Service) deleteCatalogField(ctx context.Context, subject string, d *ent.FieldDefinition) error {
	types, err := s.Client.ContentType.Query().Where(fieldTypePredicate(d.ID)).All(ctx)
	if err != nil {
		return err
	}
	for _, typ := range types {
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
			if err := s.Client.ContentType.UpdateOne(typ).SetFieldIds(ids).AddVersion(1).Exec(ctx); err != nil {
				return err
			}
		}
	}
	if err := s.Client.FieldDefinition.DeleteOne(d).Exec(ctx); err != nil {
		return err
	}
	for _, c := range collections {
		if _, e := s.Client.FieldValue.Delete().Where(fieldvalue.ContainerIDEQ(c.ID), fieldvalue.FieldKeyEQ(d.Key)).Exec(ctx); e != nil {
			return e
		}
		if _, e := s.Client.BusinessKey.Delete().Where(businesskey.ContainerIDEQ(c.ID), businesskey.FieldKeyEQ(d.Key)).Exec(ctx); e != nil {
			return e
		}
	}
	if err = s.refreshCollections(ctx, subject, collections); err != nil {
		return err
	}
	return s.auditCatalog(ctx, subject, "field.delete", map[string]any{"field_id": d.ID, "key": d.Key})
}
