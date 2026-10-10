package dms

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/fielddefinition"
	"papergo/ent/listview"
)

func (s *Service) CatalogFields(ctx context.Context, subject string) ([]*ent.FieldDefinition, error) {
	return read(ctx, s, func(t *Service) ([]*ent.FieldDefinition, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		return t.Client.FieldDefinition.Query().Order(ent.Asc(fielddefinition.FieldKey), ent.Asc(fielddefinition.FieldID)).All(ctx)
	})
}

// CreateCatalogField creates an organization field that content types reference by ID.
func (s *Service) CreateCatalogField(ctx context.Context, subject string, in CreateField) (out *ent.FieldDefinition, err error) {
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
			if e := t.normalizeItemValues(ctx, subject, []*ent.FieldDefinition{d}, map[string]any{}, nil); e != nil {
				return e
			}
		}
		var e error
		out, e = t.Client.FieldDefinition.Create().SetKey(d.Key).SetLabel(d.Label).SetType(d.Type).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetScale(d.Scale).SetOptions(d.Options).Save(ctx)
		if e != nil {
			return e
		}
		return t.auditCatalog(ctx, subject, "field.create", map[string]any{"field_id": out.ID, "key": out.Key})
	})
	return
}

func fieldTypePredicate(id string) func(*entsql.Selector) {
	return func(q *entsql.Selector) {
		q.Where(entsql.ExprP("EXISTS(SELECT 1 FROM json_each("+q.C(contenttype.FieldFieldIds)+") WHERE value=?)", id))
	}
}
func (s *Service) bumpFieldTypes(ctx context.Context, id string) error {
	_, e := s.Client.ContentType.Update().Where(fieldTypePredicate(id)).AddVersion(1).Save(ctx)
	return e
}

func (s *Service) validateSchemaViews(ctx context.Context, collection string, defs map[string]*ent.FieldDefinition) error {
	views, e := s.Client.ListView.Query().Where(listview.ContainerIDEQ(collection)).All(ctx)
	if e != nil {
		return e
	}
	for _, v := range views {
		var q QuerySpec
		if e = json.Unmarshal(v.Query, &q); e != nil {
			return e
		}
		for _, key := range v.Columns {
			if !builtinColumn(key) && defs[key] == nil {
				return invalid("remove dependent saved-view references before removing a field")
			}
		}
		if !schemaFilterKnown(q.Filter, defs) {
			return invalid("remove dependent saved-view references before removing a field")
		}
		for _, key := range []string{q.Sort.Field, q.GroupBy} {
			if key != "" && !builtinColumn(key) && defs[key] == nil {
				return invalid("remove dependent saved-view references before removing a field")
			}
		}
	}
	return nil
}
func schemaFilterKnown(f *FilterExpr, defs map[string]*ent.FieldDefinition) bool {
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

func builtinColumn(key string) bool { _, d := systemField(key); return d != nil || key == "$tags" }
