package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	viewstore "papergo/internal/dal/views"
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

func (s *Service) reviseFieldDefinition(ctx context.Context, prior, candidate *catalog.FieldDefinition) (*catalog.FieldDefinition, error) {
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
	// No content type may hide the field once forms must fill it.
	owners, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{FieldID: prior.ID, Detached: true})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(owners))
	for i, typ := range owners {
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
