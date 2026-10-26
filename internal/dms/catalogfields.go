package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	viewstore "papergo/internal/dal/views"
	"slices"
)

func (s *Service) CatalogFields(ctx context.Context, subject string) ([]*catalog.FieldDefinition, error) {
	return read(ctx, s, func(t *Service) ([]*catalog.FieldDefinition, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		return t.DAL.Catalog().Fields(ctx, catalog.FieldFilter{})
	})
}

// CreateCatalogField creates an organization field that content types reference by ID.
func (s *Service) CreateCatalogField(ctx context.Context, subject string, in CreateField) (out *catalog.FieldDefinition, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		if in.ContentTypeID != "" {
			return invalid("attach catalog fields through the content type definition")
		}
		d := fieldFromInput(in)
		if d.Choices == nil {
			d.Choices = []string{}
		}
		if e := validateFieldDefinition(d); e != nil {
			return e
		}
		if e := t.validateReferenceScopes(ctx, subject, d); e != nil {
			return e
		}
		if len(d.Options.DefaultValue) > 0 {
			if e := t.normalizeKeywordDefault(ctx, subject, d); e != nil {
				return e
			}
		}
		var e error
		out, e = t.persistFieldDefinition(ctx, d)
		if e != nil {
			return e
		}
		return t.auditCatalog(ctx, subject, "field.create", map[string]any{"field_id": out.ID, "key": out.Key})
	})
	return
}

func (s *Service) bumpFieldTypes(ctx context.Context, id string) error {
	return s.DAL.Catalog().BumpContentTypesForField(ctx, id)
}

func (s *Service) validateSchemaViews(ctx context.Context, collection string, defs map[string]*catalog.FieldDefinition) error {
	saved, e := s.DAL.Views().ListViews(ctx, viewstore.ViewFilter{ContainerID: collection})
	if e != nil {
		return e
	}
	for _, v := range saved {
		var q QuerySpec
		if e = json.Unmarshal(v.Query, &q); e != nil {
			return e
		}
		if q.ContentTypeID != "" {
			if _, e = s.contentType(ctx, collection, q.ContentTypeID); e != nil {
				return invalid("remove dependent saved-view content-type references before detaching a type")
			}
		}
		for _, key := range v.Columns {
			if !builtinColumn(key) && defs[key] == nil {
				return invalid("remove dependent saved-view references before removing a field")
			}
		}
		if !schemaFilterKnown(q.Filter, defs) {
			return invalid("remove dependent saved-view references before removing a field")
		}
		keys := []string{q.GroupBy}
		for _, order := range q.Sort {
			keys = append(keys, order.Field)
		}
		for _, key := range keys {
			if key != "" && !builtinColumn(key) && defs[key] == nil {
				return invalid("remove dependent saved-view references before removing a field")
			}
		}
	}
	return nil
}
func schemaFilterKnown(f *FilterExpr, defs map[string]*catalog.FieldDefinition) bool {
	if f == nil {
		return true
	}
	if f.Field != "" && !builtinColumn(f.Field) && defs[f.Field] == nil {
		return false
	}
	if !schemaFilterKnown(f.Not, defs) {
		return false
	}
	for _, c := range f.And {
		if !schemaFilterKnown(&c, defs) {
			return false
		}
	}
	for _, c := range f.Or {
		if !schemaFilterKnown(&c, defs) {
			return false
		}
	}
	return true
}

func builtinColumn(key string) bool { d := systemField(key); return d != nil || key == "$keywords" }

func (s *Service) persistFieldDefinition(ctx context.Context, d *catalog.FieldDefinition) (*catalog.FieldDefinition, error) {
	return s.DAL.Catalog().CreateField(ctx, *d)
}

// fieldTypes returns the content types that have a field, as their own or
// inherited, detached list types included.
func (s *Service) fieldTypes(ctx context.Context, id string) ([]*catalog.ContentType, error) {
	owners, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{FieldID: id, Detached: true})
	if err != nil || len(owners) == 0 {
		return nil, err
	}
	ids := make([]string, len(owners))
	for i, typ := range owners {
		ids[i] = typ.ID
	}
	if ids, err = s.DAL.Catalog().Descendants(ctx, ids); err != nil {
		return nil, err
	}
	return s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{IDs: ids, Detached: true})
}

// checkTypeDefaults checks that the defaults the types give a field hold for
// its definition.
func (s *Service) checkTypeDefaults(ctx context.Context, subject string, d *catalog.FieldDefinition, types []*catalog.ContentType) error {
	for _, typ := range types {
		i := slices.IndexFunc(typ.FieldSettings, func(setting catalog.FieldSetting) bool { return setting.FieldID == d.ID })
		if i < 0 || typ.FieldSettings[i].DefaultValue == nil || string(typ.FieldSettings[i].DefaultValue) == "null" {
			continue
		}
		candidate := *withSetting(d, typ.Effective.Setting(d.ID))
		if e := validateFieldDefinition(&candidate); e != nil {
			return invalid("content type " + typ.Name + " defaults " + d.Key + ": " + e.Error())
		}
		if e := s.normalizeKeywordDefault(ctx, subject, &candidate); e != nil {
			return e
		}
	}
	return nil
}

func (s *Service) reviseFieldDefinition(ctx context.Context, subject string, prior, candidate *catalog.FieldDefinition) (*catalog.FieldDefinition, error) {
	before, err := json.Marshal(prior)
	if err != nil {
		return nil, err
	}
	change := *candidate
	change.ID = prior.ID
	updated, err := s.DAL.Catalog().SaveField(ctx, change, prior.Version)
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return nil, ErrPreconditionFailed
	}
	if err != nil {
		return nil, err
	}
	// The defaults content types give the field still hold, and no type
	// may hide it once forms must fill it.
	types, err := s.fieldTypes(ctx, prior.ID)
	if err != nil {
		return nil, err
	}
	if err = s.checkTypeDefaults(ctx, subject, updated, types); err != nil {
		return nil, err
	}
	ids := make([]string, len(types))
	for i, typ := range types {
		ids[i] = typ.ID
	}
	if err = s.checkHiddenMandatory(ctx, ids); err != nil {
		return nil, err
	}
	if err := s.trackAuditProperties(prior.ID, before, updated, "label", "required", "choices", "indexed", "options"); err != nil {
		return nil, err
	}
	return updated, nil
}
