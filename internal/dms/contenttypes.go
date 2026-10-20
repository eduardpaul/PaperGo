package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/surfaces"
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
	*catalog.ContentType
	IsDefault bool `json:"is_default"`
}

func (s *Service) collectionFields(ctx context.Context, collection string) ([]*catalog.FieldDefinition, error) {
	return s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{CollectionID: collection})
}
func (s *Service) collectionTypes(ctx context.Context, collection string) ([]*catalog.ContentType, error) {
	return s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{CollectionID: collection})
}
func selectTypeDefinitions(defs []*catalog.FieldDefinition, typ *catalog.ContentType) []*catalog.FieldDefinition {
	out := []*catalog.FieldDefinition{}
	for _, d := range defs {
		if slices.Contains(typ.FieldIds, d.ID) {
			out = append(out, d)
		}
	}
	return out
}
func fieldKeys(defs []*catalog.FieldDefinition) []string {
	out := []string{}
	for _, d := range defs {
		out = append(out, d.Key)
	}
	return out
}
func (s *Service) typeDefinitions(ctx context.Context, typ *catalog.ContentType) ([]*catalog.FieldDefinition, error) {
	return s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append([]string{}, typ.FieldIds...)})
}
func (s *Service) contentType(ctx context.Context, collection, id string) (*catalog.ContentType, error) {
	out, err := s.DAL.Catalog().CollectionContentType(ctx, collection, id)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return out, err
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
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append([]string{}, in.FieldIds...)})
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
func (s *Service) CreateContentType(ctx context.Context, subject string, in ContentTypeInput) (out *catalog.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		if e := t.validateContentType(ctx, &in); e != nil {
			return e
		}
		var e error
		out, e = t.persistContentType(ctx, in, "")
		if e != nil {
			return e
		}
		return t.auditCatalog(ctx, subject, "content_type.create", map[string]any{"content_type_id": out.ID})
	})
	return
}
func (s *Service) CatalogContentTypes(ctx context.Context, subject string) ([]*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*catalog.ContentType, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		return t.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{})
	})
}
func (s *Service) ContentTypes(ctx context.Context, subject, collection string) ([]*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*catalog.ContentType, error) {
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
		defaultType, e := t.defaultContentType(ctx, collection)
		if e != nil {
			return nil, e
		}
		out := []CollectionContentType{}
		for _, typ := range types {
			out = append(out, CollectionContentType{typ, typ.ID == defaultType})
		}
		return out, nil
	})
}

// defaultContentType is the id of the content type a collection's new
// items get.
func (s *Service) defaultContentType(ctx context.Context, collection string) (string, error) {
	attachments, err := s.DAL.Catalog().Attachments(ctx, collection)
	if err != nil {
		return "", err
	}
	for _, a := range attachments {
		if a.IsDefault {
			return a.ContentTypeID, nil
		}
	}
	return "", ErrNotFound
}
func (s *Service) GetContentType(ctx context.Context, subject, id string) (*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) (*catalog.ContentType, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		typ, e := t.DAL.Catalog().ContentType(ctx, id)
		if dberr.IsNotFound(e) {
			return nil, ErrNotFound
		}
		return typ, e
	})
}

// catalogError maps catalog rules to service errors.
func catalogError(err error) error {
	var v catalog.Violation
	if errors.As(err, &v) {
		return invalid(err.Error())
	}
	return err
}

func (s *Service) auditCatalog(ctx context.Context, subject, action string, details map[string]any) error {
	return s.RecordEntity(ctx, subject, action, AuditTarget{}, details)
}
func (s *Service) attachedCollections(ctx context.Context, ids []string) ([]*resources.Resource, error) {
	collections, err := s.DAL.Catalog().AttachedCollections(ctx, ids)
	if err != nil || len(collections) == 0 {
		return nil, err
	}
	return s.DAL.Resources().Resources(ctx, resources.Filter{IDs: collections, State: resources.Live})
}
func (s *Service) fieldCollections(ctx context.Context, id string) ([]*resources.Resource, error) {
	types, e := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{FieldID: id})
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
func (s *Service) refreshCollections(ctx context.Context, subject string, collections []*resources.Resource) error {
	for _, c := range collections {
		if e := s.validateIntegrationSchema(ctx, c); e != nil {
			return e
		}
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
func (s *Service) UpdateContentType(ctx context.Context, subject, id string, version int, in ContentTypeInput) (out *catalog.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		old, e := t.DAL.Catalog().ContentType(ctx, id)
		if dberr.IsNotFound(e) {
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
		out, e = t.reviseContentType(ctx, old, in.Name, in.FieldIds, in.Rules)
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
func (s *Service) AttachContentType(ctx context.Context, subject, collection string, version int, in AttachContentTypeInput) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return e
		}
		var e error
		out, e = t.attachContentType(ctx, subject, collection, version, in)
		return e
	})
	return
}

// attachContentType also serves automatic provisioning of a fixed type. It
// enforces list Manage; choosing a catalog type through the public API requires
// contentType.read as well.
func (s *Service) attachContentType(ctx context.Context, subject, collection string, version int, in AttachContentTypeInput) (out *resources.Resource, err error) {
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
		changed, e := t.DAL.Catalog().Attach(ctx, c.ID, in.ContentTypeID, in.IsDefault)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if errors.Is(e, dberr.ErrUniqueViolation) {
			return ErrConflict
		}
		if e != nil {
			return catalogError(e)
		}
		if !changed {
			out = c
			return nil
		}
		if e = t.refreshCollections(ctx, subject, []*resources.Resource{c}); e != nil {
			return e
		}
		out, e = t.DAL.Resources().Resource(ctx, c.ID)
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
func (s *Service) DetachContentType(ctx context.Context, subject, collection, typeID string, version int) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		e = t.DAL.Catalog().Detach(ctx, collection, typeID)
		switch {
		case dberr.IsNotFound(e):
			return ErrNotFound
		case errors.Is(e, catalog.ErrLastContentType), errors.Is(e, catalog.ErrContentTypeInUse):
			return ErrConflict
		case e != nil:
			return e
		}
		if e = t.refreshCollections(ctx, subject, []*resources.Resource{c}); e != nil {
			return e
		}
		out, e = t.DAL.Resources().Resource(ctx, collection)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.detach", c, map[string]any{"content_type_id": typeID})
	})
	return
}

func selectDefinitions(defs []*catalog.FieldDefinition, keys []string) []*catalog.FieldDefinition {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := []*catalog.FieldDefinition{}
	for _, d := range defs {
		if wanted[d.Key] {
			out = append(out, d)
		}
	}
	return out
}

func typedDefinitions(raw json.RawMessage, id string) ([]*catalog.FieldDefinition, []model.ValidationRule, error) {
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

func (s *Service) checkTypeHeads(ctx context.Context, subject string, c *resources.Resource, typeID string, keys []string, rules []model.ValidationRule) error {
	defs, err := s.collectionFields(ctx, c.ID)
	if err != nil {
		return err
	}
	defs = selectDefinitions(defs, keys)
	for after := ""; ; {
		items, err := s.DAL.Resources().Resources(ctx, resources.Filter{ContainerID: c.ID, ContentTypeID: typeID, State: resources.Live, AfterID: after, Limit: surfaceBatch})
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
		heads, err := s.DAL.Surfaces().Surfaces(ctx, surfaces.Filter{ItemIDs: ids, Surface: headSurface(c)})
		if err != nil {
			return err
		}
		kinds := map[string]refs.ResourceKind{}
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

func (s *Service) persistContentType(ctx context.Context, in ContentTypeInput, templateKey string) (*catalog.ContentType, error) {
	t := catalog.ContentType{Key: in.Key, Name: in.Name, FieldIds: in.FieldIds, Rules: in.Rules}
	if templateKey != "" {
		t.TemplateKey = &templateKey
	}
	return s.DAL.Catalog().CreateContentType(ctx, t)
}

func (s *Service) reviseContentType(ctx context.Context, prior *catalog.ContentType, name string, fields []string, rules []model.ValidationRule) (*catalog.ContentType, error) {
	before, err := json.Marshal(prior)
	if err != nil {
		return nil, err
	}
	updated, err := s.DAL.Catalog().SaveContentType(ctx, catalog.ContentType{ID: prior.ID, Name: name, FieldIds: fields, Rules: rules}, prior.Version)
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return nil, ErrPreconditionFailed
	}
	if err != nil {
		return nil, err
	}
	if err := s.trackAuditProperties(prior.ID, before, updated, "name", "field_ids", "rules"); err != nil {
		return nil, err
	}
	return updated, nil
}
