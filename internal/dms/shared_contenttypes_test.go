package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/fielddefinition"
	"papergo/ent/operation"
	"papergo/internal/model"
	"testing"
)

func sharedTypeFixture(t *testing.T) (*Service, *ent.Resource, *ent.Resource, *ent.ContentType, *ent.FieldDefinition) {
	t.Helper()
	s, _, a := fixture(t)
	ctx := context.Background()
	w, e := s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Separate"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, "bob", w.ID, CreateResource{Kind: "list", Name: "Other records", PublishingEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.CreateCatalogField(ctx, "alice", CreateField{Key: "code", Label: "Code", Type: "text"})
	if e != nil {
		t.Fatal(e)
	}
	typ, e := s.CreateContentType(ctx, "alice", ContentTypeInput{Key: "invoice", Name: "Invoice", FieldIds: []string{d.ID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		r           *ent.Resource
		subject     string
		defaultType bool
	}{{a, "alice", true}, {b, "bob", false}} {
		if _, e = s.AttachContentType(ctx, v.subject, v.r.ID, v.r.Version, AttachContentTypeInput{ContentTypeID: typ.ID, IsDefault: v.defaultType}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.Get(ctx, "alice", b.ID); !errors.Is(e, ErrForbidden) {
		t.Fatal("fixture must keep curator outside second workspace", e)
	}
	return s, a, b, typ, d
}

func TestSharedContentTypesLiveFieldsDefaultsAndFrozenHistory(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	var items []*ent.Resource
	for i, v := range []struct {
		r       *ent.Resource
		subject string
	}{{a, "alice"}, {b, "bob"}} {
		r, e := s.Create(ctx, v.subject, v.r.ID, CreateResource{Kind: "item", Name: "Invoice", ContentTypeID: typ.ID, Values: map[string]any{"code": "A"}})
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 {
			_, e = s.Publish(ctx, v.subject, r.ID, r.Version)
			if e != nil {
				t.Fatal(e)
			}
			r, e = s.Get(ctx, v.subject, r.ID)
			if e != nil {
				t.Fatal(e)
			}
		}
		items = append(items, r)
	}
	plain, e := s.Create(ctx, "bob", b.ID, CreateResource{Kind: "item", Name: "Plain"})
	if e != nil || *plain.ContentTypeID == typ.ID {
		t.Fatal("default leaked between collections", plain, e)
	}
	old, e := s.Client.ItemRevision.Get(ctx, *items[1].PublishedRevisionID)
	if e != nil {
		t.Fatal(e)
	}
	frozen, e := s.Client.SchemaRevision.Get(ctx, old.SchemaRevisionID)
	if e != nil {
		t.Fatal(e)
	}
	saved := string(frozen.Definition)
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	label := "Invoice code"
	indexed := true
	d, e = s.UpdateCatalogField(ctx, "alice", d.ID, d.Version, UpdateField{Label: &label, Indexed: &indexed})
	if e != nil || d.IndexStatus != fielddefinition.IndexStatusBuilding {
		t.Fatal(d, e)
	}
	for _, v := range []struct {
		r       *ent.Resource
		subject string
		version int
	}{{a, "alice", beforeA.Version}, {b, "bob", beforeB.Version}} {
		c, e := s.Get(ctx, v.subject, v.r.ID)
		if e != nil || c.Version != v.version+1 {
			t.Fatal("schema did not advance", c, e)
		}
		defs, e := s.Fields(ctx, v.subject, c.ID)
		if e != nil || len(defs) != 1 || defs[0].ID != d.ID || defs[0].Label != label {
			t.Fatal("definition was copied", defs, e)
		}
		if _, e = s.Query(ctx, v.subject, c.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ.ID, Filter: &FilterExpr{Field: "code", Value: json.RawMessage(`"A"`)}}}); e == nil {
			t.Fatal("query used unfinished shared index")
		}
	}
	// Completing the first collection cannot make the shared field ready.
	for n := 0; n < 20; n++ {
		if _, e = s.operationStep(ctx); e != nil {
			t.Fatal(e)
		}
		finished, e := s.Client.Operation.Query().Where(operation.FieldIDEQ(d.ID), operation.StatusEQ(operation.StatusSucceeded)).Count(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if finished == 1 {
			live, e := s.Client.FieldDefinition.Get(ctx, d.ID)
			if e != nil || live.IndexStatus != fielddefinition.IndexStatusBuilding {
				t.Fatal("ready before every collection completed", live, e)
			}
			break
		}
		if n == 19 {
			t.Fatal("first index operation never completed")
		}
	}
	if e = s.runOperations(ctx); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		r       *ent.Resource
		subject string
	}{{a, "alice"}, {b, "bob"}} {
		q, e := s.Query(ctx, v.subject, v.r.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ.ID, Filter: &FilterExpr{Field: "code", Value: json.RawMessage(`"A"`)}}})
		if e != nil || len(q.Data) != 1 {
			t.Fatal(q, e)
		}
	}
	frozen, e = s.Client.SchemaRevision.Get(ctx, old.SchemaRevisionID)
	if e != nil || string(frozen.Definition) != saved {
		t.Fatal("published schema was reinterpreted", e)
	}
	// The same definition can be renamed without access to the second workspace.
	typ, e = s.GetContentType(ctx, "alice", typ.ID)
	if e != nil {
		t.Fatal(e)
	}
	typ, e = s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: "Shared invoices", FieldIds: typ.FieldIds})
	if e != nil {
		t.Fatal(e)
	}
	attached, e := s.CollectionContentTypes(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range attached {
		if v.ID == typ.ID && (v.Name != "Shared invoices" || v.IsDefault) {
			t.Fatal(v)
		}
	}
}

func TestSharedContentTypeValidationRollsBackEveryAttachment(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	if _, e := s.Create(ctx, "alice", a.ID, CreateResource{Kind: "item", Name: "Valid", ContentTypeID: typ.ID, Values: map[string]any{"code": "A"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(ctx, "bob", b.ID, CreateResource{Kind: "item", Name: "Empty", ContentTypeID: typ.ID}); e != nil {
		t.Fatal(e)
	}
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	count, e := s.Client.AuditEvent.Query().Count(ctx)
	if e != nil {
		t.Fatal(e)
	}
	required := true
	if _, e = s.UpdateCatalogField(ctx, "alice", d.ID, d.Version, UpdateField{Required: &required}); !isValidation(e) {
		t.Fatal("invalid remote head accepted", e)
	}
	live, e := s.Client.FieldDefinition.Get(ctx, d.ID)
	if e != nil || live.Required || live.Version != d.Version {
		t.Fatal("field change leaked", live, e)
	}
	for _, before := range []*ent.Resource{beforeA, beforeB} {
		after, e := s.Client.Resource.Get(ctx, before.ID)
		if e != nil || after.Version != before.Version || *after.SchemaHeadID != *before.SchemaHeadID {
			t.Fatal("schema change leaked", after, e)
		}
	}
	if after, e := s.Client.AuditEvent.Query().Count(ctx); e != nil || after != count {
		t.Fatal("audit escaped rollback", after, e)
	}
	rules := []model.ValidationRule{{Key: "different", Field: "code", Op: "ne", OtherField: "code", Message: "Codes must differ"}}
	if _, e = s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, FieldIds: typ.FieldIds, Rules: rules}); !isValidation(e) {
		t.Fatal(e)
	}
	if current, e := s.GetContentType(ctx, "bob", typ.ID); e != nil || current.Version != typ.Version || len(current.Rules) != 0 {
		t.Fatal(current, e)
	}
	if _, e = s.UpdateCatalogField(ctx, "bob", d.ID, d.Version, UpdateField{Required: &required}); !errors.Is(e, ErrForbidden) {
		t.Fatal("member could curate", e)
	}
	if _, e = s.CreateContentType(ctx, "bob", ContentTypeInput{Key: "private", Name: "Private"}); !errors.Is(e, ErrForbidden) {
		t.Fatal("member could create catalog definition", e)
	}
}
