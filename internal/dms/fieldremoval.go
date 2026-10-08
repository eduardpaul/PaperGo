package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/businesskey"
	"papergo/ent/contenttype"
	"papergo/ent/fieldvalue"
	"papergo/ent/listview"
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
		c, err := t.authorize(ctx, subject, collection, "manage")
		if err != nil {
			return err
		}
		if c.Version != version {
			return ErrConflict
		}
		d, err := t.Client.FieldDefinition.Get(ctx, id)
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if d.ContainerID != c.ID {
			return ErrNotFound
		}
		types, err := t.Client.ContentType.Query().Where(contenttype.ContainerIDEQ(c.ID)).All(ctx)
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
		for after := ""; ; {
			views, err := t.Client.ListView.Query().Where(listview.ContainerIDEQ(c.ID), listview.IDGT(after)).Order(ent.Asc(listview.FieldID)).Limit(surfaceBatch).All(ctx)
			if err != nil {
				return err
			}
			for _, view := range views {
				var query QuerySpec
				if err := json.Unmarshal(view.Query, &query); err != nil {
					return err
				}
				if slices.Contains(view.Columns, d.Key) || query.Sort.Field == d.Key || query.GroupBy == d.Key || filterUsesField(query.Filter, d.Key) {
					return invalid("remove dependent saved-view references before deleting this field")
				}
			}
			if len(views) < surfaceBatch {
				break
			}
			after = views[len(views)-1].ID
		}
		for _, typ := range types {
			if slices.Contains(typ.FieldKeys, d.Key) {
				keys := slices.DeleteFunc(slices.Clone(typ.FieldKeys), func(key string) bool { return key == d.Key })
				if err := t.Client.ContentType.UpdateOne(typ).SetFieldKeys(keys).AddVersion(1).Exec(ctx); err != nil {
					return err
				}
			}
		}
		if err := t.Client.FieldDefinition.DeleteOne(d).Exec(ctx); err != nil {
			return err
		}
		if _, err := t.Client.FieldValue.Delete().Where(fieldvalue.ContainerIDEQ(c.ID), fieldvalue.FieldKeyEQ(d.Key)).Exec(ctx); err != nil {
			return err
		}
		if _, err := t.Client.BusinessKey.Delete().Where(businesskey.ContainerIDEQ(c.ID), businesskey.FieldKeyEQ(d.Key)).Exec(ctx); err != nil {
			return err
		}
		if err := t.recordSchema(ctx, subject, c); err != nil {
			return err
		}
		return t.audit(ctx, subject, "field.delete", c, map[string]any{"field_id": id, "key": d.Key})
	})
}
