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

// rootTypeKey is the key of the root catalog type, Item, that every other
// type derives from.
const rootTypeKey = "item"

// ContentTypeInput defines a catalog type. A new type derives from ParentID,
// or Item when it is empty, and the parent never changes. FieldIds,
// HiddenFieldIds and Rules are the type's own; it also has everything its
// ancestors define. Hidden fields are inherited fields forms leave out.
type ContentTypeInput struct {
	Key            string                 `json:"key"`
	Name           string                 `json:"name"`
	ParentID       string                 `json:"parent_id,omitempty"`
	FieldIds       []string               `json:"field_ids"`
	HiddenFieldIds []string               `json:"hidden_field_ids,omitempty"`
	Rules          []model.ValidationRule `json:"rules,omitempty"`
}

// ListTypeInput customizes a collection's list type with fields, hidden
// fields and rules of its own.
type ListTypeInput struct {
	FieldIds       []string               `json:"field_ids"`
	HiddenFieldIds []string               `json:"hidden_field_ids,omitempty"`
	Rules          []model.ValidationRule `json:"rules,omitempty"`
}

// AttachContentTypeInput names the catalog type a collection accepts.
type AttachContentTypeInput struct {
	ContentTypeID string `json:"content_type_id"`
	IsDefault     bool   `json:"is_default"`
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
		if slices.Contains(typ.Effective.FieldIds, d.ID) {
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
	return s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append([]string{}, typ.Effective.FieldIds...)})
}

// contentType is a list type a collection accepts, or its default when id is
// empty.
func (s *Service) contentType(ctx context.Context, collection, id string) (*catalog.ContentType, error) {
	out, err := s.DAL.Catalog().CollectionContentType(ctx, collection, id)
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return out, err
}

// listType is the list type through which a collection accepts a catalog
// type.
func (s *Service) listType(ctx context.Context, collection, catalogID string) (*catalog.ContentType, error) {
	typ, err := s.DAL.Catalog().OneContentType(ctx, catalog.ContentTypeFilter{CollectionID: collection, ParentID: catalogID})
	if dberr.IsNotFound(err) {
		return nil, ErrNotFound
	}
	return typ, err
}

// typeFamily resolves a catalog type, or a list type of a collection, to
// itself and the collection's list types deriving from the catalog type: a
// list type stands for its catalog type, so its subtypes match too.
func (s *Service) typeFamily(ctx context.Context, collection, id string) (*catalog.ContentType, []*catalog.ContentType, error) {
	typ, err := s.DAL.Catalog().ContentType(ctx, id)
	if dberr.IsNotFound(err) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if !typ.Catalog() {
		if *typ.CollectionID != collection {
			return nil, nil, ErrNotFound
		}
		id = *typ.ParentID
	}
	types, err := s.collectionTypes(ctx, collection)
	if err != nil {
		return nil, nil, err
	}
	family := slices.DeleteFunc(types, func(t *catalog.ContentType) bool { return !slices.Contains(t.AncestorIDs, id) })
	if len(family) == 0 {
		return nil, nil, ErrNotFound
	}
	return typ, family, nil
}

// ancestorTypes loads the ancestors of types, nearest first, by type id.
func (s *Service) ancestorTypes(ctx context.Context, types []*catalog.ContentType) (map[string][]*catalog.ContentType, error) {
	ids := []string{}
	for _, typ := range types {
		ids = append(ids, typ.AncestorIDs...)
	}
	rows, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{IDs: ids})
	if err != nil {
		return nil, err
	}
	byID := map[string]*catalog.ContentType{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := map[string][]*catalog.ContentType{}
	for _, typ := range types {
		for _, id := range typ.AncestorIDs {
			if a := byID[id]; a != nil {
				out[typ.ID] = append(out[typ.ID], a)
			}
		}
	}
	return out, nil
}

// catalogType is a catalog type; list types are not part of the catalog.
func (s *Service) catalogType(ctx context.Context, id string) (*catalog.ContentType, error) {
	typ, err := s.DAL.Catalog().ContentType(ctx, id)
	if dberr.IsNotFound(err) || err == nil && !typ.Catalog() {
		return nil, ErrNotFound
	}
	return typ, err
}

func (s *Service) validateTypeName(in ContentTypeInput) error {
	if !fieldKey.MatchString(in.Key) {
		return invalid("invalid content type key")
	}
	return validateName(in.Name)
}

// validateTypeSchema checks what a type defines itself against what its
// parent gives it. Own fields exist and are not inherited; hidden fields are
// inherited and can be left out of forms; every field key is distinct and
// the rules hold over all the fields.
func (s *Service) validateTypeSchema(ctx context.Context, parent *catalog.ContentType, in *ListTypeInput) error {
	in.FieldIds, in.HiddenFieldIds, in.Rules = nonNil(in.FieldIds), nonNil(in.HiddenFieldIds), nonNil(in.Rules)
	inherited, inheritedRules := []string{}, []model.ValidationRule{}
	if parent != nil {
		inherited, inheritedRules = parent.Effective.FieldIds, parent.Effective.Rules
	}
	if len(inherited)+len(in.FieldIds) > 200 {
		return invalid("at most 200 fields per content type")
	}
	own, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append([]string{}, in.FieldIds...)})
	if err != nil {
		return err
	}
	if len(own) != len(in.FieldIds) {
		return invalid("content type field IDs must exist and be unique")
	}
	defs, err := s.DAL.Catalog().Fields(ctx, catalog.FieldFilter{IDs: append(slices.Clone(inherited), in.FieldIds...)})
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, d := range defs {
		if seen[d.Key] {
			return invalid("content type field keys must be unique, including inherited fields: " + d.Key)
		}
		seen[d.Key] = true
		if slices.Contains(in.FieldIds, d.ID) && slices.Contains(inherited, d.ID) {
			return invalid("the content type inherits field " + d.Key)
		}
	}
	for _, id := range in.HiddenFieldIds {
		i := slices.IndexFunc(defs, func(d *catalog.FieldDefinition) bool { return d.ID == id })
		if i < 0 || !slices.Contains(inherited, id) {
			return invalid("hidden fields must be inherited fields")
		}
		if defs[i].Required && len(defs[i].Options.DefaultValue) == 0 {
			return invalid("a required field without a default cannot be hidden: " + defs[i].Key)
		}
	}
	for _, rule := range in.Rules {
		if slices.ContainsFunc(inheritedRules, func(r model.ValidationRule) bool { return r.Key == rule.Key }) {
			return invalid("the content type inherits rule " + rule.Key)
		}
	}
	return validateRules(defs, append(slices.Clone(inheritedRules), in.Rules...))
}

// reservedTypeKey reports whether a catalog key belongs to Item or to a
// content type of a registered collection template.
func (s *Service) reservedTypeKey(key string) bool {
	if key == rootTypeKey {
		return true
	}
	for _, template := range s.collectionTemplates {
		for _, typ := range template.ContentTypes {
			if typ.Key == key {
				return true
			}
		}
	}
	return false
}

// rootType provisions the root catalog type, Item, on first use.
func (s *Service) rootType(ctx context.Context, subject string) (*catalog.ContentType, error) {
	typ, err := s.DAL.Catalog().OneContentType(ctx, catalog.ContentTypeFilter{TemplateKey: rootTypeKey})
	if !dberr.IsNotFound(err) {
		return typ, err
	}
	key := rootTypeKey
	if typ, err = s.DAL.Catalog().CreateContentType(ctx, catalog.ContentType{Key: rootTypeKey, Name: "Item", TemplateKey: &key}); err != nil {
		return nil, err
	}
	return typ, s.auditCatalog(ctx, subject, "content_type.create", map[string]any{"content_type_id": typ.ID, "template_key": rootTypeKey})
}

func (s *Service) CreateContentType(ctx context.Context, subject string, in ContentTypeInput) (out *catalog.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		if e := t.validateTypeName(in); e != nil {
			return e
		}
		if t.reservedTypeKey(in.Key) {
			return invalid("content type key is reserved by a template: " + in.Key)
		}
		var parent *catalog.ContentType
		var e error
		if in.ParentID == "" {
			parent, e = t.rootType(ctx, subject)
		} else {
			parent, e = t.catalogType(ctx, in.ParentID)
		}
		if e != nil {
			return e
		}
		schema := ListTypeInput{FieldIds: in.FieldIds, HiddenFieldIds: in.HiddenFieldIds, Rules: in.Rules}
		if e = t.validateTypeSchema(ctx, parent, &schema); e != nil {
			return e
		}
		out, e = t.DAL.Catalog().CreateContentType(ctx, catalog.ContentType{Key: in.Key, Name: in.Name, ParentID: &parent.ID, FieldIds: schema.FieldIds, HiddenFieldIds: schema.HiddenFieldIds, Rules: schema.Rules})
		if errors.Is(e, dberr.ErrUniqueViolation) {
			return invalid("content type key is taken: " + in.Key)
		}
		if e != nil {
			return catalogError(e)
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
		return t.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{Catalog: true})
	})
}

// CollectionContentTypes lists the list types a collection accepts.
func (s *Service) CollectionContentTypes(ctx context.Context, subject, collection string) ([]*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) ([]*catalog.ContentType, error) {
		if _, e := t.contentTypeResource(ctx, subject, collection, "read"); e != nil {
			return nil, e
		}
		return t.collectionTypes(ctx, collection)
	})
}

// CollectionContentType reads a list type of a collection, by its id or the
// catalog type it copies.
func (s *Service) CollectionContentType(ctx context.Context, subject, collection, id string) (*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) (*catalog.ContentType, error) {
		if _, e := t.authorize(ctx, subject, collection, "read"); e != nil {
			return nil, e
		}
		return t.contentType(ctx, collection, id)
	})
}

// defaultContentType is the id of the list type a collection's new items
// get.
func (s *Service) defaultContentType(ctx context.Context, collection string) (string, error) {
	typ, err := s.contentType(ctx, collection, "")
	if err != nil {
		return "", err
	}
	return typ.ID, nil
}
func (s *Service) GetContentType(ctx context.Context, subject, id string) (*catalog.ContentType, error) {
	return read(ctx, s, func(t *Service) (*catalog.ContentType, error) {
		if e := t.RequireScope(ctx, subject, "contentType.read"); e != nil {
			return nil, e
		}
		return t.catalogType(ctx, id)
	})
}

// catalogError maps catalog rules to service errors.
func catalogError(err error) error {
	var v catalog.Violation
	if errors.As(err, &v) || errors.Is(err, dberr.ErrInvalidReference) {
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
			selected := selectTypeDefinitions(defs, typ)
			if e = validateRules(selected, typ.Effective.Rules); e != nil {
				return e
			}
			if e = s.checkTypeHeads(ctx, subject, c, typ.ID, fieldKeys(selected), typ.Effective.Rules); e != nil {
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

// checkSubtypes checks that the rules of a type's catalog subtypes still
// hold over their fields; refreshCollections checks the list types.
func (s *Service) checkSubtypes(ctx context.Context, id string) error {
	ids, err := s.DAL.Catalog().Descendants(ctx, []string{id})
	if err != nil {
		return err
	}
	types, err := s.DAL.Catalog().ContentTypes(ctx, catalog.ContentTypeFilter{IDs: ids, Catalog: true})
	if err != nil {
		return err
	}
	for _, typ := range types {
		defs, err := s.typeDefinitions(ctx, typ)
		if err != nil {
			return err
		}
		if err = validateRules(defs, typ.Effective.Rules); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateContentType(ctx context.Context, subject, id string, version int, in ContentTypeInput) (out *catalog.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "contentType.manage"); e != nil {
			return e
		}
		old, e := t.catalogType(ctx, id)
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
		if in.ParentID != "" && (old.ParentID == nil || in.ParentID != *old.ParentID) {
			return invalid("content type parent is immutable")
		}
		if e = t.validateTypeName(in); e != nil {
			return e
		}
		var parent *catalog.ContentType
		if old.ParentID != nil {
			if parent, e = t.DAL.Catalog().ContentType(ctx, *old.ParentID); e != nil {
				return e
			}
		}
		schema := ListTypeInput{FieldIds: in.FieldIds, HiddenFieldIds: in.HiddenFieldIds, Rules: in.Rules}
		if e = t.validateTypeSchema(ctx, parent, &schema); e != nil {
			return e
		}
		collections, e := t.attachedCollections(ctx, []string{id})
		if e != nil {
			return e
		}
		reshaped, e := changedSchema(old, schema)
		if e != nil {
			return e
		}
		out, e = t.reviseContentType(ctx, old, catalog.ContentType{Name: in.Name, FieldIds: schema.FieldIds, HiddenFieldIds: schema.HiddenFieldIds, Rules: schema.Rules})
		if e != nil {
			return e
		}
		// Fields and rules reach the items of every type deriving from this
		// one; a new name reaches only the collections' schema snapshots.
		switch {
		case reshaped:
			if e = t.checkSubtypes(ctx, id); e != nil {
				return e
			}
			if e = t.refreshCollections(ctx, subject, collections); e != nil {
				return e
			}
		case out.Name != old.Name:
			for _, c := range collections {
				if e = t.recordSchema(ctx, subject, c); e != nil {
					return e
				}
			}
		}
		return t.auditCatalog(ctx, subject, "content_type.update", map[string]any{"content_type_id": id, "version": out.Version})
	})
	return
}

// changedSchema reports whether a write changes a type's own fields or rules.
func changedSchema(old *catalog.ContentType, next ListTypeInput) (bool, error) {
	if !slices.Equal(old.FieldIds, next.FieldIds) {
		return true, nil
	}
	before, err := json.Marshal(nonNil(old.Rules))
	if err != nil {
		return false, err
	}
	after, err := json.Marshal(nonNil(next.Rules))
	return string(before) != string(after), err
}

// UpdateListType customizes a collection's list type at its version. The
// type keeps every field and rule of its catalog type.
func (s *Service) UpdateListType(ctx context.Context, subject, collection, id string, version int, in ListTypeInput) (out *catalog.ContentType, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		typ, e := t.contentType(ctx, c.ID, id)
		if e != nil {
			return e
		}
		if typ.Version != version {
			return ErrPreconditionFailed
		}
		parent, e := t.DAL.Catalog().ContentType(ctx, *typ.ParentID)
		if e != nil {
			return e
		}
		if e = t.validateTypeSchema(ctx, parent, &in); e != nil {
			return e
		}
		if out, e = t.reviseContentType(ctx, typ, catalog.ContentType{FieldIds: in.FieldIds, HiddenFieldIds: in.HiddenFieldIds, Rules: in.Rules}); e != nil {
			return e
		}
		if e = t.refreshCollections(ctx, subject, []*resources.Resource{c}); e != nil {
			return e
		}
		return t.audit(ctx, subject, "content_type.update", c, map[string]any{"content_type_id": id, "version": out.Version})
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
// contentType.read as well. The collection accepts the catalog type through
// its own list type.
func (s *Service) attachContentType(ctx context.Context, subject, collection string, version int, in AttachContentTypeInput) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		if e := t.RequireScope(ctx, subject, "item.read"); e != nil {
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
		listType, changed, e := t.DAL.Catalog().Attach(ctx, c.ID, in.ContentTypeID, in.IsDefault)
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
		return t.audit(ctx, subject, "content_type.attach", c, map[string]any{"content_type_id": in.ContentTypeID, "list_type_id": listType.ID, "is_default": in.IsDefault})
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

// DetachContentType makes a collection stop accepting a list type. The list
// type stays for frozen revisions and returns when its catalog type is
// attached again.
func (s *Service) DetachContentType(ctx context.Context, subject, collection, typeID string, version int) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, collection, "manage")
		if e != nil {
			return e
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		// Like the other list type routes, the type may be named by the
		// catalog type it copies.
		typ, e := t.contentType(ctx, c.ID, typeID)
		if e != nil {
			return e
		}
		e = t.DAL.Catalog().Detach(ctx, collection, typ.ID)
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
		return t.audit(ctx, subject, "content_type.detach", c, map[string]any{"content_type_id": typ.ID})
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

func (s *Service) reviseContentType(ctx context.Context, prior *catalog.ContentType, next catalog.ContentType) (*catalog.ContentType, error) {
	before, err := json.Marshal(prior)
	if err != nil {
		return nil, err
	}
	next.ID = prior.ID
	if next.Name == "" {
		next.Name = prior.Name
	}
	updated, err := s.DAL.Catalog().SaveContentType(ctx, next, prior.Version)
	if errors.Is(err, dberr.ErrConcurrentUpdate) {
		return nil, ErrPreconditionFailed
	}
	if err != nil {
		return nil, catalogError(err)
	}
	if err := s.trackAuditProperties(prior.ID, before, updated, "name", "field_ids", "hidden_field_ids", "rules"); err != nil {
		return nil, err
	}
	return updated, nil
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
