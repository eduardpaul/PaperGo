package dms

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/ent/contenttype"
	"papergo/internal/model"
	"reflect"
	"sort"
)

// CollectionTemplate provisions a complete collection. Modules register templates
// before serving requests; the first content type is the collection default.
type CollectionTemplate struct {
	Key               string          `json:"key"`
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Kind              string          `json:"kind"`
	AllowFolders      bool            `json:"allow_folders"`
	Versioning        string          `json:"versioning"`
	MaxVersions       int             `json:"max_versions"`
	PublishingEnabled bool            `json:"publishing_enabled"`
	WebDAVEnabled     bool            `json:"webdav_enabled"`
	ContentTypes      []TemplateInput `json:"content_types"`
	Views             []ViewInput     `json:"views"`
}

func builtInCollectionTemplates() map[string]CollectionTemplate {
	return map[string]CollectionTemplate{
		"documents": {Key: "documents", Name: "Documents", Description: "Document library with descriptions and keywords", Kind: "library", AllowFolders: true, Versioning: "major", MaxVersions: 50,
			ContentTypes: []TemplateInput{{Key: "document", Name: "Document", Fields: []CreateField{
				{Key: "description", Label: "Description", Type: "note"},
				{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}},
			}}}, Views: []ViewInput{{Name: "All documents", Columns: []string{"$name", "description", "keywords"}, Layout: "table", IsDefault: true}}},
		"contacts": {Key: "contacts", Name: "Contacts", Description: "Contact list", Kind: "list", AllowFolders: true, Versioning: "off", MaxVersions: 50,
			ContentTypes: []TemplateInput{{Key: "contact", Name: "Contact", Fields: []CreateField{
				{Key: "email", Label: "Email", Type: "email"}, {Key: "phone", Label: "Phone", Type: "text"},
				{Key: "company", Label: "Company", Type: "text"}, {Key: "job_title", Label: "Job title", Type: "text"}, {Key: "notes", Label: "Notes", Type: "note"},
			}}}, Views: []ViewInput{{Name: "All contacts", Columns: []string{"$name", "company", "email", "phone"}, Query: QuerySpec{Sort: []SortSpec{{Field: "$name", Direction: "asc"}}}, Layout: "table", IsDefault: true}}},
	}
}

// RegisterCollectionTemplate takes an owned copy; duplicate keys are errors.
func (s *Service) RegisterCollectionTemplate(in CollectionTemplate) error {
	if !fieldKey.MatchString(in.Key) {
		return invalid("invalid collection template key")
	}
	if _, exists := s.collectionTemplates[in.Key]; exists {
		return invalid("duplicate collection template key")
	}
	if err := validateContainerMetadata(in.Name, in.Description); err != nil {
		return err
	}
	if in.Kind != "list" && in.Kind != "library" {
		return invalid("collection template kind must be list or library")
	}
	if in.Versioning == "" {
		in.Versioning = "off"
		if in.Kind == "library" {
			in.Versioning = "major"
		}
	}
	if in.MaxVersions == 0 {
		in.MaxVersions = 50
	}
	if err := validateHistorySettings(true, &in.Versioning, &in.MaxVersions); err != nil {
		return err
	}
	if in.WebDAVEnabled && in.Kind != "library" {
		return invalid("WebDAV requires a library")
	}
	if len(in.ContentTypes) == 0 || len(in.ContentTypes) > 32 {
		return invalid("collection template needs 1 to 32 content types")
	}
	if len(in.Views) > 50 {
		return invalid("at most 50 template views")
	}
	seen := map[string]bool{}
	for _, typ := range in.ContentTypes {
		if !fieldKey.MatchString(typ.Key) || len(typ.Fields) > 200 {
			return invalid("invalid template content type")
		}
		if err := validateName(typ.Name); err != nil {
			return err
		}
		defs := []*ent.FieldDefinition{}
		fields := map[string]bool{}
		for _, f := range typ.Fields {
			if f.ContentTypeID != "" || fields[f.Key] {
				return invalid("template fields must be unique and cannot select a type")
			}
			fields[f.Key] = true
			d := fieldFromInput(f)
			if err := validateFieldDefinition(d); err != nil {
				return err
			}
			defs = append(defs, d)
		}
		if err := validateRules(defs, typ.Rules); err != nil {
			return err
		}
		if seen[typ.Key] {
			return invalid("duplicate template content type")
		}
		seen[typ.Key] = true
		for _, existing := range s.collectionTemplates {
			for _, other := range existing.ContentTypes {
				if other.Key == typ.Key && !reflect.DeepEqual(other, typ) {
					return invalid("conflicting template content type key")
				}
			}
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	var owned CollectionTemplate
	if err = json.Unmarshal(raw, &owned); err != nil {
		return err
	}
	s.collectionTemplates[in.Key] = owned
	return nil
}

func (s *Service) CollectionTemplates(ctx context.Context, subject string) ([]CollectionTemplate, error) {
	if err := s.RequireScope(ctx, subject, "list.read"); err != nil {
		return nil, err
	}
	out := []CollectionTemplate{}
	for _, template := range s.collectionTemplates {
		out = append(out, template)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var owned []CollectionTemplate
	err = json.Unmarshal(raw, &owned)
	return owned, err
}

func (s *Service) provisionCollectionTypes(ctx context.Context, subject string, template CollectionTemplate) ([]string, error) {
	ids := []string{}
	shared := map[string]*ent.FieldDefinition{}
	// Load existing shared types first so new types reuse their live field IDs.
	for _, recipe := range template.ContentTypes {
		typ, err := s.Client.ContentType.Query().Where(contenttype.TemplateKeyEQ(recipe.Key)).Only(ctx)
		if ent.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		defs, err := s.typeDefinitions(ctx, typ)
		if err != nil {
			return nil, err
		}
		for _, d := range defs {
			if old := shared[d.Key]; old != nil && old.ID != d.ID {
				return nil, invalid("template types have conflicting live field keys")
			}
			shared[d.Key] = d
		}
	}
	for _, recipe := range template.ContentTypes {
		typ, err := s.Client.ContentType.Query().Where(contenttype.TemplateKeyEQ(recipe.Key)).Only(ctx)
		if err == nil {
			ids = append(ids, typ.ID)
			continue
		}
		if !ent.IsNotFound(err) {
			return nil, err
		}
		raw, err := s.templateDefinition(ctx, subject, "", recipe)
		if err != nil {
			return nil, err
		}
		defs, err := definitions(raw)
		if err != nil {
			return nil, err
		}
		fieldIDs := []string{}
		for _, d := range defs {
			live := shared[d.Key]
			if live != nil {
				if live.Type != d.Type || live.Scale != d.Scale || !optionsIdentityEqual(live.Options, d.Options) {
					return nil, invalid("template field identity conflicts: " + d.Key)
				}
			} else {
				live, err = s.Client.FieldDefinition.Create().SetKey(d.Key).SetLabel(d.Label).SetType(d.Type).SetRequired(d.Required).SetChoices(d.Choices).SetIndexed(d.Indexed).SetScale(d.Scale).SetOptions(d.Options).Save(ctx)
				if err != nil {
					return nil, err
				}
				shared[d.Key] = live
				if err = s.auditCatalog(ctx, subject, "field.create", map[string]any{"field_id": live.ID, "template_key": recipe.Key}); err != nil {
					return nil, err
				}
			}
			fieldIDs = append(fieldIDs, live.ID)
		}
		candidate := ContentTypeInput{Key: recipe.Key, Name: recipe.Name, FieldIds: fieldIDs, Rules: recipe.Rules}
		if err = s.validateContentType(ctx, &candidate); err != nil {
			return nil, err
		}
		typ, err = s.Client.ContentType.Create().SetKey(recipe.Key).SetTemplateKey(recipe.Key).SetName(recipe.Name).SetFieldIds(fieldIDs).SetRules(candidate.Rules).Save(ctx)
		if err != nil {
			return nil, err
		}
		if err = s.auditCatalog(ctx, subject, "content_type.create", map[string]any{"content_type_id": typ.ID, "template_key": recipe.Key}); err != nil {
			return nil, err
		}
		ids = append(ids, typ.ID)
	}
	return ids, nil
}
