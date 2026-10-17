package dms

import (
	"context"
	"errors"
	"papergo/ent/contenttype"
	"papergo/ent/resource"
	"papergo/internal/model"
	"testing"
)

func TestCollectionTemplatesProvisionShareAndPreserveTypes(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := context.Background()
	a, err := s.Create(ctx, "alice", w.ID, CreateResource{Name: "Contacts", TemplateKey: "contacts"})
	if err != nil {
		t.Fatal(err)
	}
	types, err := s.CollectionContentTypes(ctx, "alice", a.ID)
	if err != nil || len(types) != 1 || !types[0].IsDefault {
		t.Fatal(types, err)
	}
	typ := types[0].ContentType
	if typ.TemplateKey == nil || *typ.TemplateKey != "contact" || len(typ.FieldIds) != 5 {
		t.Fatal(typ)
	}
	_, err = s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Key: typ.Key, Name: "People", FieldIds: typ.FieldIds})
	if err != nil {
		t.Fatal(err)
	}
	private, err := s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "bob", private.ID, CreateResource{Name: "People", TemplateKey: "contacts"})
	if err != nil {
		t.Fatal(err)
	}
	bt, err := s.CollectionContentTypes(ctx, "bob", b.ID)
	if err != nil || bt[0].ID != typ.ID || bt[0].Name != "People" {
		t.Fatal(bt, err)
	}
	views, err := s.Views(ctx, "bob", b.ID, "", 50)
	if err != nil || len(views.Data) != 1 || !views.Data[0].IsDefault || len(views.Data[0].Columns) != 4 {
		t.Fatal(views, err)
	}
	item, err := s.Create(ctx, "bob", b.ID, CreateResource{Kind: "item", Name: "Ada", Values: map[string]any{"email": "ada@example.com"}})
	if err != nil || item.ContentTypeID == nil || *item.ContentTypeID != typ.ID {
		t.Fatal(item, err)
	}
	docs, err := s.Create(ctx, "alice", w.ID, CreateResource{Kind: "list", Name: "Files", TemplateKey: "documents", AllowFolders: ptr(false)})
	if err != nil || docs.Kind != "library" || !docs.AllowFolders {
		t.Fatal(docs, err)
	}
	fields, err := s.Fields(ctx, "alice", docs.ID)
	if err != nil || len(fields) != 1 || fields[0].Key != "description" {
		t.Fatal("document template must use intrinsic keywords instead of adding a duplicate field", fields, err)
	}
	if _, err = s.Create(ctx, "alice", docs.ID, CreateResource{Kind: "item", Name: "a.txt", Keywords: []string{"alpha", "beta"}, Values: map[string]any{"description": "Example"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCollectionTemplateAtomicFailureAndAccess(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := context.Background()
	recipe := CollectionTemplate{Key: "module", Name: "Module", Kind: "list", PublishingEnabled: true, ContentTypes: []TemplateInput{{Key: "module_type", Name: "Module type", Fields: []CreateField{{Key: "code", Label: "Code", Type: "text", Options: model.FieldOptions{DefaultValue: []byte(`"A"`)}}}}}, Views: []ViewInput{{Name: "Bad view", Columns: []string{"unknown"}, IsDefault: true}}}
	if err := s.RegisterCollectionTemplate(recipe); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Client.FieldDefinition.Query().Count(ctx)
	if _, err := s.Create(ctx, "alice", w.ID, CreateResource{Name: "Failed", TemplateKey: "module"}); err == nil {
		t.Fatal("invalid view accepted")
	}
	after, _ := s.Client.FieldDefinition.Query().Count(ctx)
	exists, _ := s.Client.ContentType.Query().Where(contenttype.TemplateKeyEQ("module_type")).Exist(ctx)
	resources, _ := s.Client.Resource.Query().Where(resource.NameEQ("Failed")).Count(ctx)
	if before != after || exists || resources != 0 {
		t.Fatal("provisioning leaked", before, after, exists, resources)
	}
	if _, err := s.Create(ctx, "bob", w.ID, CreateResource{Name: "Denied", TemplateKey: "contacts"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, in := range []CreateResource{{Name: "Unknown", TemplateKey: "unknown"}, {Kind: "item", Name: "Invalid", TemplateKey: "contacts"}, {Name: "Mixed", TemplateKey: "contacts", ContentTypeIDs: []string{"x"}}} {
		if _, err := s.Create(ctx, "alice", w.ID, in); err == nil {
			t.Fatal("invalid input accepted", in)
		}
	}
	recipe.Key = "valid_module"
	recipe.Views = []ViewInput{{Name: "Codes", Columns: []string{"code"}, IsDefault: true}}
	if err := s.RegisterCollectionTemplate(recipe); err != nil {
		t.Fatal(err)
	}
	recipe.ContentTypes[0].Fields[0].Label = "Mutated"
	c, err := s.Create(ctx, "alice", w.ID, CreateResource{Name: "Module", TemplateKey: "valid_module"})
	if err != nil || !c.PublishingEnabled {
		t.Fatal(c, err)
	}
	if _, err = s.Create(ctx, "alice", c.ID, CreateResource{Kind: "item", Name: "Default"}); err != nil {
		t.Fatal(err)
	}
}

func TestModuleCollectionTemplateSharesFieldsAcrossTypes(t *testing.T) {
	s, w, _ := fixture(t)
	field := CreateField{Key: "common", Label: "Common", Type: "text"}
	template := CollectionTemplate{Key: "module_multi", Name: "Module multi", Kind: "list", ContentTypes: []TemplateInput{{Key: "module_a", Name: "A", Fields: []CreateField{field}}, {Key: "module_b", Name: "B", Fields: []CreateField{field, {Key: "extra", Label: "Extra", Type: "integer"}}}}, Views: []ViewInput{{Name: "All", Columns: []string{"common", "extra"}, IsDefault: true}}}
	if e := s.RegisterCollectionTemplate(template); e != nil {
		t.Fatal(e)
	}
	c, e := s.Create(testContext, "alice", w.ID, CreateResource{Name: "Multi", TemplateKey: template.Key})
	if e != nil {
		t.Fatal(e)
	}
	types, e := s.CollectionContentTypes(testContext, "alice", c.ID)
	if e != nil || len(types) != 2 {
		t.Fatal(types, e)
	}
	if types[0].FieldIds[0] != types[1].FieldIds[0] {
		t.Fatal("shared key copied", types)
	}
	defs, e := s.Fields(testContext, "alice", c.ID)
	if e != nil || len(defs) != 2 {
		t.Fatal(defs, e)
	}
}
