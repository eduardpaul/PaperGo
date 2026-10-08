package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/fielddefinition"
	"papergo/ent/itemsurface"
	"papergo/ent/resource"
	"papergo/internal/model"
	"slices"
)

type ContentTypeInput struct {
	Key       string                 `json:"key" required:"false" pattern:"^[a-z][a-z0-9_]{0,63}$" doc:"Required for creation, immutable. May be omitted for replacement."`
	Name      string                 `json:"name" minLength:"1" maxLength:"255"`
	FieldKeys []string               `json:"field_keys" maxItems:"200" uniqueItems:"true" doc:"Keys from the shared collection field catalog."`
	Rules     []model.ValidationRule `json:"rules,omitempty" maxItems:"32"`
	IsDefault bool                   `json:"is_default" required:"false" doc:"Selecting a default advances the former default type version."`
}

type SchemaContentType struct {
	ID        string                 `json:"id"`
	Key       string                 `json:"key"`
	Name      string                 `json:"name"`
	FieldKeys []string               `json:"field_keys"`
	Rules     []model.ValidationRule `json:"rules"`
	IsDefault bool                   `json:"is_default"`
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

func (s *Service) contentType(ctx context.Context, collection, id string) (*ent.ContentType, error) {
	q := s.Client.ContentType.Query().Where(contenttype.ContainerIDEQ(collection))
	if id == "" {
		q.Where(contenttype.IsDefaultEQ(true))
	} else {
		q.Where(contenttype.IDEQ(id))
	}
	typ, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return typ, err
}

func (s *Service) validateContentType(ctx context.Context, collection string, in *ContentTypeInput) error {
	if !fieldKey.MatchString(in.Key) {
		return invalid("invalid content type key")
	}
	if err := validateName(in.Name); err != nil {
		return err
	}
	if len(in.FieldKeys) > 200 {
		return invalid("at most 200 fields per content type")
	}
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(collection)).All(ctx)
	if err != nil {
		return err
	}
	known := definitionMap(defs)
	seen := map[string]bool{}
	for _, key := range in.FieldKeys {
		if known[key] == nil || seen[key] {
			return invalid("content type fields must be unique collection field keys")
		}
		seen[key] = true
	}
	if in.FieldKeys == nil {
		in.FieldKeys = []string{}
	}
	if in.Rules == nil {
		in.Rules = []model.ValidationRule{}
	}
	return validateRules(selectDefinitions(defs, in.FieldKeys), in.Rules)
}

func (s *Service) setDefaultType(ctx context.Context, collection, except string) error {
	_, err := s.Client.ContentType.Update().Where(contenttype.ContainerIDEQ(collection), contenttype.IDNEQ(except), contenttype.IsDefaultEQ(true)).SetIsDefault(false).AddVersion(1).Save(ctx)
	return err
}

func (s *Service) CreateContentType(ctx context.Context, subject, collection string, version int, in ContentTypeInput) (out *ent.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		if c.Kind != "list" && c.Kind != "library" {
			return invalid("content types belong to collections")
		}
		if c.Version != version {
			return ErrConflict
		}
		if e = t.validateContentType(ctx, c.ID, &in); e != nil {
			return e
		}
		n, e := t.Client.ContentType.Query().Where(contenttype.ContainerIDEQ(c.ID)).Count(ctx)
		if e != nil {
			return e
		}
		if n >= 32 {
			return invalid("at most 32 content types per collection")
		}
		if in.IsDefault {
			if e = t.setDefaultType(ctx, c.ID, ""); e != nil {
				return e
			}
		}
		out, e = t.Client.ContentType.Create().SetContainerID(c.ID).SetKey(in.Key).SetName(in.Name).SetFieldKeys(in.FieldKeys).SetRules(in.Rules).SetIsDefault(in.IsDefault).Save(ctx)
		if e != nil {
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.create", c, map[string]any{"content_type_id": out.ID})
	})
	return
}

func (s *Service) ContentTypes(ctx context.Context, subject, collection string) ([]*ent.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*ent.ContentType, error) {
		if _, err := t.authorize(ctx, subject, collection, "read"); err != nil {
			return nil, err
		}
		return t.Client.ContentType.Query().Where(contenttype.ContainerIDEQ(collection)).Order(ent.Asc(contenttype.FieldKey)).All(ctx)
	})
}

func (s *Service) GetContentType(ctx context.Context, subject, id string) (*ent.ContentType, error) {
	return read(ctx, s, func(t *Service) (*ent.ContentType, error) {
		typ, err := t.Client.ContentType.Get(ctx, id)
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		if _, err = t.authorize(ctx, subject, typ.ContainerID, "read"); err != nil {
			return nil, err
		}
		return typ, nil
	})
}

func (s *Service) UpdateContentType(ctx context.Context, subject, id string, version int, in ContentTypeInput) (out *ent.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.Client.ContentType.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		c, e := t.authorize(ctx, subject, old.ContainerID, "manage")
		if e != nil {
			return e
		}
		if old.Version != version {
			return ErrConflict
		}
		if in.Key == "" {
			in.Key = old.Key
		}
		if in.Key != old.Key {
			return invalid("content type key is immutable")
		}
		if old.IsDefault && !in.IsDefault {
			return invalid("assign another default content type before clearing this default")
		}
		if e = t.validateContentType(ctx, c.ID, &in); e != nil {
			return e
		}
		if e = t.checkTypeHeads(ctx, subject, c, old.ID, in.FieldKeys, in.Rules); e != nil {
			return e
		}
		if in.IsDefault {
			if e = t.setDefaultType(ctx, c.ID, id); e != nil {
				return e
			}
		}
		out, e = t.Client.ContentType.UpdateOne(old).SetName(in.Name).SetFieldKeys(in.FieldKeys).SetRules(in.Rules).SetIsDefault(in.IsDefault).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.update", c, map[string]any{"content_type_id": id, "version": out.Version})
	})
	return
}

func (s *Service) checkTypeHeads(ctx context.Context, subject string, c *ent.Resource, typeID string, keys []string, rules []model.ValidationRule) error {
	defs, err := s.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(c.ID)).All(ctx)
	if err != nil {
		return err
	}
	defs = selectDefinitions(defs, keys)
	for after := ""; ; {
		items, err := s.Client.Resource.Query().Where(resource.ContainerIDEQ(c.ID), resource.ContentTypeIDEQ(typeID), resource.DeletedAtIsNil(), resource.IDGT(after)).Order(ent.Asc(resource.FieldID)).Limit(surfaceBatch).Select(resource.FieldID).All(ctx)
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
		for _, head := range heads {
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
			// Reference grants can change independently; schema adoption validates
			// stored values without requiring managers to impersonate their authors.
			if e = normalizeRevisionValues(defs, projected, values); e != nil {
				return e
			}
			if e = evaluateRules(defs, rules, projected); e != nil {
				return e
			}
		}
		if len(items) < surfaceBatch {
			return nil
		}
		after = items[len(items)-1].ID
	}
}
