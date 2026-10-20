package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/model"
	"slices"
)

type TemplateInput struct {
	Key         string                 `json:"key"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Fields      []CreateField          `json:"fields"`
	Rules       []model.ValidationRule `json:"rules,omitempty"`
}

func (s *Service) templateDefinition(ctx context.Context, subject, workspaceID string, in TemplateInput) (json.RawMessage, error) {
	if !fieldKey.MatchString(in.Key) {
		return nil, invalid("invalid template key")
	}
	if e := validateName(in.Name); e != nil {
		return nil, e
	}
	if e := validateDescription(in.Description); e != nil {
		return nil, e
	}
	if len(in.Fields) > 200 {
		return nil, invalid("at most 200 fields per template")
	}
	seen := map[string]bool{}
	schema := SchemaDefinition{Fields: []SchemaField{}}
	for _, f := range in.Fields {
		if f.ContentTypeID != "" {
			return nil, invalid("template fields cannot select a content type")
		}
		d := fieldFromInput(f)
		if seen[d.Key] {
			return nil, invalid("duplicate template field")
		}
		seen[d.Key] = true
		if d.Choices == nil {
			d.Choices = []string{}
		}
		if e := validateFieldDefinition(d); e != nil {
			return nil, e
		}
		if e := s.validateReferenceScopes(ctx, subject, d); e != nil {
			return nil, e
		}
		if len(d.Options.DefaultValue) > 0 {
			if e := s.normalizeKeywordDefault(ctx, subject, d); e != nil {
				return nil, e
			}
		}
		schema.Fields = append(schema.Fields, schemaField(d))
	}
	schema.Rules = in.Rules
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	defs, err := definitions(raw)
	if err != nil {
		return nil, err
	}
	if err = validateRules(defs, in.Rules); err != nil {
		return nil, err
	}
	return raw, nil
}
func (s *Service) CreateTemplate(ctx context.Context, subject, workspaceID string, in TemplateInput) (out *catalog.Template, err error) {
	err = s.write(ctx, func(t *Service) error {
		w, e := t.schemaWorkspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		raw, e := t.templateDefinition(ctx, subject, w.ID, in)
		if e != nil {
			return e
		}
		out, e = t.DAL.Catalog().CreateTemplate(ctx, catalog.Template{WorkspaceID: w.ID, Key: in.Key, Name: in.Name, Description: in.Description, Definition: raw})
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.create", w, map[string]any{"template_id": out.ID})
	})
	return
}
func (s *Service) UpdateTemplate(ctx context.Context, subject, id string, version int, in TemplateInput) (out *catalog.Template, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.DAL.Catalog().Template(ctx, id)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		w, e := t.schemaWorkspace(ctx, subject, old.WorkspaceID, "manage")
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
			return invalid("template key is immutable")
		}
		raw, e := t.templateDefinition(ctx, subject, w.ID, in)
		if e != nil {
			return e
		}
		out, e = t.DAL.Catalog().SaveTemplate(ctx, catalog.Template{ID: id, Name: in.Name, Description: in.Description, Definition: raw}, version)
		if errors.Is(e, dberr.ErrConcurrentUpdate) {
			return ErrPreconditionFailed
		}
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.update", w, map[string]any{"template_id": id, "version": out.Version})
	})
	return
}
func (s *Service) Templates(ctx context.Context, subject, workspaceID, after string, limit int) (Page[*catalog.Template], error) {
	return read(ctx, s, func(t *Service) (Page[*catalog.Template], error) {
		if _, e := t.schemaWorkspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*catalog.Template]{}, e
		}
		rows, e := t.DAL.Catalog().Templates(ctx, catalog.TemplateFilter{WorkspaceID: workspaceID, AfterID: after, Limit: pageSize(limit) + 1})
		return entityPage(rows, limit, func(v *catalog.Template) string { return v.ID }), e
	})
}

type ApplyTemplateInput struct {
	TemplateVersion int    `json:"template_version"`
	ContentTypeID   string `json:"content_type_id,omitempty"`
}

func (s *Service) ApplyTemplate(ctx context.Context, subject, containerID, templateID string, version, templateVersion int, typeID string) (out *resources.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.contentTypeResource(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if c.Kind != "list" && c.Kind != "library" {
			return invalid("templates apply to collections")
		}
		if c.Version != version {
			return ErrPreconditionFailed
		}
		typ, e := t.contentType(ctx, c.ID, typeID)
		if e != nil {
			return e
		}
		tpl, e := t.DAL.Catalog().Template(ctx, templateID)
		if dberr.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if tpl.WorkspaceID != c.WorkspaceID {
			return invalid("template belongs to a different workspace")
		}
		if tpl.Version != templateVersion {
			return ErrPreconditionFailed
		}
		defs, e := definitions(tpl.Definition)
		if e != nil {
			return e
		}
		var recipe SchemaDefinition
		if e = json.Unmarshal(tpl.Definition, &recipe); e != nil {
			return e
		}
		required := []string{}
		reindex := []*catalog.FieldDefinition{}
		for _, d := range defs {
			if e = validateFieldDefinition(d); e != nil {
				return e
			}
			if e = t.validateReferenceScopes(ctx, subject, d); e != nil {
				return e
			}
			if len(d.Options.DefaultValue) > 0 {
				if e = t.normalizeKeywordDefault(ctx, subject, d); e != nil {
					return e
				}
			}
			old, e := t.DAL.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: c.ID, Keys: []string{d.Key}})
			if dberr.IsNotFound(e) {
				_, e = t.createField(ctx, subject, c, CreateField{ContentTypeID: typ.ID, Key: d.Key, Label: d.Label, Type: d.Type, Required: d.Required, Choices: d.Choices, Indexed: d.Indexed, Scale: d.Scale, Options: d.Options})
				if e != nil {
					return e
				}
				continue
			}
			if e != nil {
				return e
			}
			if old.Type != d.Type || old.Scale != d.Scale || !optionsIdentityEqual(old.Options, d.Options) {
				return invalid("template conflicts with existing field identity: " + d.Key)
			}
			if newlyRequired(old, d) {
				required = append(required, d.Key)
			}
			updated, e := t.reviseFieldDefinition(ctx, old, d)
			if e != nil {
				return e
			}
			if old.Indexed != d.Indexed {
				reindex = append(reindex, updated)
			}
		}
		if e = t.checkRequiredFilled(ctx, c.ID, required); e != nil {
			return e
		}
		typ, e = t.DAL.Catalog().ContentType(ctx, typ.ID)
		if e != nil {
			return e
		}
		ids := slices.Clone(typ.FieldIds)
		for _, d := range defs {
			live, e := t.DAL.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: c.ID, Keys: []string{d.Key}})
			if e != nil {
				return e
			}
			if !slices.Contains(ids, live.ID) {
				ids = append(ids, live.ID)
			}
		}
		rules := slices.Clone(typ.Rules)
		for _, rule := range recipe.Rules {
			i := slices.IndexFunc(rules, func(old model.ValidationRule) bool { return old.Key == rule.Key })
			if i < 0 {
				rules = append(rules, rule)
			} else {
				rules[i] = rule
			}
		}
		candidate := ContentTypeInput{Key: typ.Key, Name: typ.Name, FieldIds: ids, Rules: rules}
		if e = t.validateContentType(ctx, &candidate); e != nil {
			return e
		}
		if e = t.checkTypeHeads(ctx, subject, c, typ.ID, fieldKeys(defs), rules); e != nil {
			return e
		}
		if _, e = t.reviseContentType(ctx, typ, typ.Name, ids, candidate.Rules); e != nil {
			return e
		}
		affected, e := t.attachedCollections(ctx, []string{typ.ID})
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, a := range affected {
			seen[a.ID] = true
		}
		for _, d := range defs {
			live, e := t.DAL.Catalog().OneField(ctx, catalog.FieldFilter{CollectionID: c.ID, Keys: []string{d.Key}})
			if e != nil {
				return e
			}
			if e = t.bumpFieldTypes(ctx, live.ID); e != nil {
				return e
			}
			all, e := t.fieldCollections(ctx, live.ID)
			if e != nil {
				return e
			}
			for _, a := range all {
				if !seen[a.ID] {
					seen[a.ID] = true
					affected = append(affected, a)
				}
			}
		}
		if e = t.refreshCollections(ctx, subject, affected); e != nil {
			return e
		}
		for _, d := range reindex {
			if e = t.enqueueFieldIndex(ctx, subject, d); e != nil {
				return e
			}
		}
		if e = t.rebuildBusinessKeys(ctx, c.ID); e != nil {
			return e
		}
		if !slices.Contains(c.TemplateKeys, tpl.Key) {
			if len(c.TemplateKeys) >= 100 {
				return invalid("at most 100 adopted template keys per collection")
			}
			if e = t.DAL.Resources().SetTemplateKeys(ctx, c.ID, append(slices.Clone(c.TemplateKeys), tpl.Key)); e != nil {
				return e
			}
		}
		out, e = t.DAL.Resources().Resource(ctx, c.ID)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.apply", c, map[string]any{"template_id": tpl.ID, "template_version": tpl.Version, "content_type_id": typ.ID, "schema_revision_id": out.SchemaHeadID})
	})
	return
}
