package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/ent/fielddefinition"
	"papergo/ent/schematemplate"
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
		if e := s.validateReferenceScopes(ctx, subject, workspaceID, d); e != nil {
			return nil, e
		}
		if len(d.Options.DefaultValue) > 0 {
			if e := s.normalizeItemValues(ctx, subject, workspaceID, []*ent.FieldDefinition{d}, map[string]any{}, nil); e != nil {
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
func (s *Service) CreateTemplate(ctx context.Context, subject, workspaceID string, in TemplateInput) (out *ent.SchemaTemplate, err error) {
	err = s.write(ctx, func(t *Service) error {
		w, e := t.workspace(ctx, subject, workspaceID, "manage")
		if e != nil {
			return e
		}
		raw, e := t.templateDefinition(ctx, subject, w.ID, in)
		if e != nil {
			return e
		}
		out, e = t.Client.SchemaTemplate.Create().SetWorkspaceID(w.ID).SetKey(in.Key).SetName(in.Name).SetDescription(in.Description).SetDefinition(raw).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.create", w, map[string]any{"template_id": out.ID})
	})
	return
}
func (s *Service) UpdateTemplate(ctx context.Context, subject, id string, version int, in TemplateInput) (out *ent.SchemaTemplate, err error) {
	err = s.write(ctx, func(t *Service) error {
		old, e := t.Client.SchemaTemplate.Get(ctx, id)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		w, e := t.workspace(ctx, subject, old.WorkspaceID, "manage")
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
			return invalid("template key is immutable")
		}
		raw, e := t.templateDefinition(ctx, subject, w.ID, in)
		if e != nil {
			return e
		}
		out, e = t.Client.SchemaTemplate.UpdateOne(old).SetName(in.Name).SetDescription(in.Description).SetDefinition(raw).AddVersion(1).Save(ctx)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.update", w, map[string]any{"template_id": id, "version": out.Version})
	})
	return
}
func (s *Service) Templates(ctx context.Context, subject, workspaceID, after string, limit int) (Page[*ent.SchemaTemplate], error) {
	return read(ctx, s, func(t *Service) (Page[*ent.SchemaTemplate], error) {
		if _, e := t.workspace(ctx, subject, workspaceID, "read"); e != nil {
			return Page[*ent.SchemaTemplate]{}, e
		}
		rows, e := t.Client.SchemaTemplate.Query().Where(schematemplate.WorkspaceIDEQ(workspaceID), schematemplate.IDGT(after)).Order(ent.Asc(schematemplate.FieldID)).Limit(pageSize(limit) + 1).All(ctx)
		return entityPage(rows, limit, func(v *ent.SchemaTemplate) string { return v.ID }), e
	})
}

type ApplyTemplateInput struct {
	TemplateVersion int    `json:"template_version"`
	ContentTypeID   string `json:"content_type_id,omitempty"`
}

func (s *Service) ApplyTemplate(ctx context.Context, subject, containerID, templateID string, version, templateVersion int, typeID string) (out *ent.Resource, err error) {
	err = s.write(ctx, func(t *Service) error {
		c, e := t.authorize(ctx, subject, containerID, "manage")
		if e != nil {
			return e
		}
		if c.Kind != "list" && c.Kind != "library" {
			return invalid("templates apply to collections")
		}
		if c.Version != version {
			return ErrConflict
		}
		typ, e := t.contentType(ctx, c.ID, typeID)
		if e != nil {
			return e
		}
		tpl, e := t.Client.SchemaTemplate.Get(ctx, templateID)
		if ent.IsNotFound(e) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if tpl.WorkspaceID != c.WorkspaceID {
			return invalid("template belongs to a different workspace")
		}
		if tpl.Version != templateVersion {
			return ErrConflict
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
		reindex := false
		for _, d := range defs {
			if e = validateFieldDefinition(d); e != nil {
				return e
			}
			if e = t.validateReferenceScopes(ctx, subject, c.WorkspaceID, d); e != nil {
				return e
			}
			if len(d.Options.DefaultValue) > 0 {
				if e = t.normalizeItemValues(ctx, subject, c.WorkspaceID, []*ent.FieldDefinition{d}, map[string]any{}, nil); e != nil {
					return e
				}
			}
			old, e := t.Client.FieldDefinition.Query().Where(fielddefinition.ContainerIDEQ(c.ID), fielddefinition.KeyEQ(d.Key)).Only(ctx)
			if ent.IsNotFound(e) {
				_, e = t.createField(ctx, subject, c, CreateField{ContentTypeID: typ.ID, Key: d.Key, Label: d.Label, Type: string(d.Type), Required: d.Required, Choices: d.Choices, Indexed: d.Indexed, Scale: d.Scale, Options: d.Options})
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
			reindex = reindex || old.Indexed != d.Indexed
			if _, e = t.Client.FieldDefinition.UpdateOne(old).SetLabel(d.Label).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetOptions(d.Options).Save(ctx); e != nil {
				return e
			}
		}
		if e = t.checkRequiredFilled(ctx, c.ID, required); e != nil {
			return e
		}
		typ, e = t.Client.ContentType.Get(ctx, typ.ID)
		if e != nil {
			return e
		}
		keys := slices.Clone(typ.FieldKeys)
		for _, d := range defs {
			if !slices.Contains(keys, d.Key) {
				keys = append(keys, d.Key)
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
		candidate := ContentTypeInput{Key: typ.Key, Name: typ.Name, FieldKeys: keys, Rules: rules, IsDefault: typ.IsDefault}
		if e = t.validateContentType(ctx, c.ID, &candidate); e != nil {
			return e
		}
		if e = t.checkTypeHeads(ctx, subject, c, typ.ID, keys, rules); e != nil {
			return e
		}
		if _, e = t.Client.ContentType.Update().Where(contenttype.IDEQ(typ.ID)).SetFieldKeys(keys).SetRules(candidate.Rules).AddVersion(1).Save(ctx); e != nil {
			return e
		}
		if e = t.recordSchema(ctx, subject, c); e != nil {
			return e
		}
		if reindex {
			if e = t.rebuildSurfaces(ctx, c.ID); e != nil {
				return e
			}
		}
		if e = t.rebuildBusinessKeys(ctx, c.ID); e != nil {
			return e
		}
		out, e = t.Client.Resource.Get(ctx, c.ID)
		if e != nil {
			return e
		}
		return t.audit(ctx, subject, "template.apply", c, map[string]any{"template_id": tpl.ID, "template_version": tpl.Version, "content_type_id": typ.ID, "schema_revision_id": out.SchemaHeadID})
	})
	return
}
