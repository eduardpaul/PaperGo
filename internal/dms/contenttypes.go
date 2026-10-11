package dms

import (
	"context"
	"encoding/json"
	entsql "entgo.io/ent/dialect/sql"
	"papergo/ent"
	"papergo/ent/collectiontype"
	"papergo/ent/contenttype"
	"papergo/ent/fielddefinition"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"papergo/internal/model"
	"slices"
)

// Definitions and fields belong to the organization; defaults belong to attachments.
type ContentTypeInput struct {
	Key      string                 `json:"key"`
	Name     string                 `json:"name"`
	FieldIds []string               `json:"field_ids"`
	Rules    []model.ValidationRule `json:"rules,omitempty"`
}
type AttachContentTypeInput struct {
	ContentTypeID string `json:"content_type_id"`
	IsDefault     bool   `json:"is_default"`
}
type CollectionContentType struct {
	*ent.ContentType
	IsDefault bool `json:"is_default"`
}

func collectionFieldPredicate(collection string) func(*entsql.Selector) {
	return func(q *entsql.Selector) {
		q.Where(entsql.ExprP(q.C(fielddefinition.FieldID)+" IN (SELECT f.value FROM collection_types a JOIN content_types t ON t.id=a.content_type_id, json_each(t.field_ids) f WHERE a.container_id=?)", collection))
	}
}
func collectionTypePredicate(collection string) func(*entsql.Selector) {
	return func(q *entsql.Selector) {
		q.Where(entsql.ExprP(q.C(contenttype.FieldID)+" IN (SELECT content_type_id FROM collection_types WHERE container_id=?)", collection))
	}
}
func (s *Service) collectionFields(ctx context.Context, collection string) ([]*ent.FieldDefinition, error) {
	return s.Client.FieldDefinition.Query().Where(collectionFieldPredicate(collection)).Order(ent.Asc(fielddefinition.FieldKey), ent.Asc(fielddefinition.FieldID)).All(ctx)
}
func (s *Service) collectionTypes(ctx context.Context, collection string) ([]*ent.ContentType, error) {
	return s.Client.ContentType.Query().Where(collectionTypePredicate(collection)).Order(ent.Asc(contenttype.FieldKey)).All(ctx)
}
func selectTypeDefinitions(defs []*ent.FieldDefinition, typ *ent.ContentType) []*ent.FieldDefinition {
	out := []*ent.FieldDefinition{}
	for _, d := range defs {
		if slices.Contains(typ.FieldIds, d.ID) {
			out = append(out, d)
		}
	}
	return out
}
func fieldKeys(defs []*ent.FieldDefinition) []string {
	out := []string{}
	for _, d := range defs {
		out = append(out, d.Key)
	}
	return out
}
func (s *Service) typeDefinitions(ctx context.Context, typ *ent.ContentType) ([]*ent.FieldDefinition, error) {
	return s.Client.FieldDefinition.Query().Where(fielddefinition.IDIn(typ.FieldIds...)).Order(ent.Asc(fielddefinition.FieldKey)).All(ctx)
}
func (s *Service) contentType(ctx context.Context, collection, id string) (*ent.ContentType, error) {
	q := s.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(collection))
	if id == "" {
		q.Where(collectiontype.IsDefaultEQ(true))
	} else {
		q.Where(collectiontype.ContentTypeIDEQ(id))
	}
	a, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.Client.ContentType.Get(ctx, a.ContentTypeID)
}
func (s *Service) validateContentType(ctx context.Context, in *ContentTypeInput) error {
	if !fieldKey.MatchString(in.Key) {
		return invalid("invalid content type key")
	}
	if err := validateName(in.Name); err != nil {
		return err
	}
	if len(in.FieldIds) > 200 {
		return invalid("at most 200 fields per content type")
	}
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.IDIn(in.FieldIds...)).All(ctx)
	if err != nil {
		return err
	}
	if len(defs) != len(in.FieldIds) {
		return invalid("content type field IDs must exist and be unique")
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.Key] {
			return invalid("content type field keys must be unique")
		}
		seen[d.Key] = true
	}
	if in.FieldIds == nil {
		in.FieldIds = []string{}
	}
	if in.Rules == nil {
		in.Rules = []model.ValidationRule{}
	}
	return validateRules(defs, in.Rules)
}
func (s *Service) CreateContentType(ctx context.Context, subject string, in ContentTypeInput) (out *ent.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		if e := t.validateContentType(ctx, &in); e != nil {
			return e
		}
		var e error
		out, e = t.Client.ContentType.Create().SetKey(in.Key).SetName(in.Name).SetFieldIds(in.FieldIds).SetRules(in.Rules).Save(ctx)
		if e != nil {
			return e
		}
		return t.auditCatalog(ctx, subject, "content_type.create", map[string]any{"content_type_id": out.ID})
	})
	return
}
func (s *Service) CatalogContentTypes(ctx context.Context, subject string) ([]*ent.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*ent.ContentType, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		return t.Client.ContentType.Query().Order(ent.Asc(contenttype.FieldKey), ent.Asc(contenttype.FieldID)).All(ctx)
	})
}
func (s *Service) ContentTypes(ctx context.Context, subject, collection string) ([]*ent.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*ent.ContentType, error) {
		if _, e := t.contentTypeResource(ctx, subject, collection, "read"); e != nil {
			return nil, e
		}
		return t.collectionTypes(ctx, collection)
	})
}
func (s *Service) CollectionContentTypes(ctx context.Context, subject, collection string) ([]CollectionContentType, error) {
	return read(ctx, s, func(t *Service) ([]CollectionContentType, error) {
		types, e := t.ContentTypes(ctx, subject, collection)
		if e != nil {
			return nil, e
		}
		a, e := t.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(collection), collectiontype.IsDefaultEQ(true)).Only(ctx)
		if e != nil {
			return nil, e
		}
		out := []CollectionContentType{}
		for _, typ := range types {
			out = append(out, CollectionContentType{typ, typ.ID == a.ContentTypeID})
		}
		return out, nil
	})
}
func (s *Service) GetContentType(ctx context.Context, subject, id string) (*ent.ContentType, error) {
	return read(ctx, s, func(t *Service) (*ent.ContentType, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		typ, e := t.Client.ContentType.Get(ctx, id)
		if ent.IsNotFound(e) {
			return nil, ErrNotFound
		}
		return typ, e
	})
}
func (s *Service) auditCatalog(ctx context.Context, subject, action string, details map[string]any) error {
	typ, id := auditEntity(action, nil, details)
	return s.Client.AuditEvent.Create().SetSubject(subject).SetAction(action).SetEntityType(typ).SetEntityID(id).SetProperties(s.changedProperties(id, action)).SetTraceID(s.traceID).SetDetails(details).Exec(ctx)
}
func (s *Service) attachedCollections(ctx context.Context, ids []string) ([]*ent.Resource, error) {
	return s.Client.Resource.Query().Where(resource.DeletedAtIsNil(), func(q *entsql.Selector) {
		b := entsql.Select("container_id").From(entsql.Table("collection_types")).Where(entsql.In("content_type_id", stringArgs(ids)...))
		q.Where(entsql.In(q.C(resource.FieldID), b))
	}).Order(ent.Asc(resource.FieldID)).All(ctx)
}
func stringArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
func (s *Service) fieldCollections(ctx context.Context, id string) ([]*ent.Resource, error) {
	types, e := s.Client.ContentType.Query().Where(func(q *entsql.Selector) {
		q.Where(entsql.ExprP("EXISTS(SELECT 1 FROM json_each("+q.C(contenttype.FieldFieldIds)+") WHERE value=?)", id))
	}).All(ctx)
	if e != nil {
		return nil, e
	}
	ids := []string{}
	for _, typ := range types {
		ids = append(ids, typ.ID)
	}
	return s.attachedCollections(ctx, ids)
}

// Every attached schema advances in the writer transaction. No collection can
// adopt a partial catalog update, including collections the curator cannot read.
func (s *Service) refreshCollections(ctx context.Context, subject string, collections []*ent.Resource) error {
	for _, c := range collections {
		defs, e := s.collectionFields(ctx, c.ID)
		if e != nil {
			return e
		}
		seen := map[string]string{}
		for _, d := range defs {
			if old := seen[d.Key]; old != "" && old != d.ID {
				return invalid("attached content types have conflicting field keys: " + d.Key)
			}
			seen[d.Key] = d.ID
		}
		if len(defs) > 200 {
			return invalid("at most 200 fields per collection")
		}
		if e = s.validateFieldHistory(ctx, c.ID, defs); e != nil {
			return e
		}
		types, e := s.collectionTypes(ctx, c.ID)
		if e != nil {
			return e
		}
		for _, typ := range types {
			if e = s.checkTypeHeads(ctx, subject, c, typ.ID, fieldKeys(selectTypeDefinitions(defs, typ)), typ.Rules); e != nil {
				return e
			}
		}
		if e = s.validateSchemaViews(ctx, c.ID, definitionMap(defs)); e != nil {
			return e
		}
		if e = s.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		if e = s.rebuildBusinessKeys(ctx, c.ID); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) UpdateContentType(ctx context.Context, subject, id string, version int, in ContentTypeInput) (out *ent.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		old, e := t.Client.ContentType.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrPreconditionFailed
		}
		if in.Key == "" {
			in.Key = old.Key
		}
		if in.Key != old.Key {
			return invalid("content type key is immutable")
		}
		if e = t.validateContentType(ctx, &in); e != nil {
			return e
		}
		collections, e := t.attachedCollections(ctx, []string{id})
		if e != nil {
			return e
		}
		out, e = t.Client.ContentType.UpdateOne(old).SetName(in.Name).SetFieldIds(in.FieldIds).SetRules(in.Rules).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if e = t.refreshCollections(ctx, subject, collections); e != nil {
			return e
		}
		return t.auditCatalog(ctx, subject, "content_type.update", map[string]any{"content_type_id": id, "version": out.Version})
	})
	return
}
func (s *Service) AttachContentType(ctx context.Context, subject, collection string, version int, in AttachContentTypeInput) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "list.read"); e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		if c.Kind != "list" && c.Kind != "library" {
			return invalid("content types attach to collections")
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		if e = t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return e
		}
		if _, e = t.Client.ContentType.Get(ctx, in.ContentTypeID); ent.IsNotFound(e) {
			return ErrNotFound
		} else if e != nil {
			return e
		}
		count, e := t.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(c.ID)).Count(ctx)
		if e != nil {
			return e
		}
		a, e := t.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(c.ID), collectiontype.ContentTypeIDEQ(in.ContentTypeID)).Only(ctx)
		if e != nil && !ent.IsNotFound(e) {
			return e
		}
		if a != nil && (!in.IsDefault || a.IsDefault) {
			out = c
			return nil
		}
		if in.IsDefault {
			if _, e = t.Client.CollectionType.Update().Where(collectiontype.ContainerIDEQ(c.ID), collectiontype.IsDefaultEQ(true)).SetIsDefault(false).Save(ctx); e != nil {
				return e
			}
		}
		if a == nil {
			if count >= 32 {
				return invalid("at most 32 content types per collection")
			}
			if _, e = t.Client.CollectionType.Create().SetContainerID(c.ID).SetContentTypeID(in.ContentTypeID).SetIsDefault(in.IsDefault).Save(ctx); e != nil {
				return e
			}
		} else if in.IsDefault {
			if e = t.Client.CollectionType.UpdateOne(a).SetIsDefault(true).Exec(ctx); e != nil {
				return e
			}
		}
		if e = t.refreshCollections(ctx, subject, []*ent.Resource{c}); e != nil {
			return e
		}
		out, e = t.Client.Resource.Get(ctx, c.ID)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.attach", c, map[string]any{"content_type_id": in.ContentTypeID, "is_default": in.IsDefault})
	})
	return
}

type SchemaContentType struct {
	ID        string                 `json:"id"`
	Key       string                 `json:"key"`
	Name      string                 `json:"name"`
	FieldKeys []string               `json:"field_keys"`
	Rules     []model.ValidationRule `json:"rules"`
	IsDefault bool                   `json:"is_default"`
}

// DetachContentType removes only this collection's attachment. Frozen revision
// schemas and the shared organization catalog remain available.
func (s *Service) DetachContentType(ctx context.Context, subject, collection, typeID string, version int) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		a, e := t.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(collection), collectiontype.ContentTypeIDEQ(typeID)).Only(ctx)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		remaining, e := t.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(collection), collectiontype.IDNEQ(a.ID)).Order(ent.Asc(collectiontype.FieldContentTypeID)).All(ctx)
		if e != nil {
			return e
		}
		if len(remaining) == 0 {
			return ErrConflict
		}
		inUse, e := t.Client.Resource.Query().Where(resource.ContainerIDEQ(collection), resource.ContentTypeIDEQ(typeID), resource.DeletedAtIsNil()).Exist(ctx)
		if e != nil {
			return e
		}
		if inUse {
			return ErrConflict
		}
		if e = t.Client.CollectionType.DeleteOne(a).Exec(ctx); e != nil {
			return e
		}
		if a.IsDefault {
			if e = t.Client.CollectionType.UpdateOne(remaining[0]).SetIsDefault(true).Exec(ctx); e != nil {
				return e
			}
		}
		if e = t.refreshCollections(ctx, subject, []*ent.Resource{c}); e != nil {
			return e
		}
		out, e = t.Client.Resource.Get(ctx, collection)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.detach", c, map[string]any{"content_type_id": typeID})
	})
	return
}

func selectDefinitions(defs []*ent.FieldDefinition, keys []string) []*ent.FieldDefinition {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := []*ent.FieldDefinition{}
	for _, d := range defs {
		if wanted[d.Key] {
			out = append(out, d)
		}
	}
	return out
}

func typedDefinitions(raw json.RawMessage, id string) ([]*ent.FieldDefinition, []model.ValidationRule, error) {
	var schema SchemaDefinition
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, nil, err
	}
	defs, err := definitions(raw)
	if err != nil {
		return nil, nil, err
	}
	for _, typ := range schema.ContentTypes {
		if typ.ID == id {
			return selectDefinitions(defs, typ.FieldKeys), typ.Rules, nil
		}
	}
	return nil, nil, invalid("content type is missing from the effective schema")
}

func (s *Service) checkTypeHeads(ctx context.Context, subject string, c *ent.Resource, typeID string, keys []string, rules []model.ValidationRule) error {
	defs, err := s.collectionFields(ctx, c.ID)
	if err != nil {
		return err
	}
	defs = selectDefinitions(defs, keys)
	for after := ""; ; {
		items, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.ContentTypeIDEQ(typeID), resource.DeletedAtIsNil(), resource.IDGT(after)).Order(ent.Asc(resource.FieldID)).Limit(surfaceBatch).Select(resource.FieldID, resource.FieldKind).All(ctx)
		if err != nil {
			return err
		}
		ids := []string{}
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if len(ids) == 0 {
			return nil
		}
		heads, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDIn(ids...), itemsurface.SurfaceEQ(itemsurface.SurfaceHead)).All(ctx)
		if err != nil {
			return err
		}
		kinds := map[string]resource.Kind{}
		for _, item := range items {
			kinds[item.ID] = item.Kind
		}
		for _, head := range heads {
			selected, selectedRules := resourceDefinitions(kinds[head.ItemID], defs, rules)
			values, e := decodeValues(head.Payload)
			if e != nil {
				return e
			}
			projected := map[string]any{}
			for key, value := range values {
				if slices.Contains(keys, key) {
					projected[key] = value
				}
			}
			for _, d := range selected {
				if d.Required && len(d.Options.DefaultValue) == 0 && emptyValue(projected[d.Key]) {
					return invalid("required field missing: " + d.Key)
				}
			}
			// Reference grants can change independently; schema adoption validates
			// stored values without requiring managers to impersonate their authors.
			if e = normalizeRevisionValues(selected, projected, values, false); e != nil {
				return e
			}
			if e = evaluateRules(selected, selectedRules, projected); e != nil {
				return e
			}
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}
