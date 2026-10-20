package dms

import (
	"context"
	"encoding/json"
	"errors"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resources"
	"papergo/internal/model"
	"sync"
	"testing"
)

func sharedTypeFixture(t *testing.T) (*Service, *resources.Resource, *resources.Resource, *catalog.ContentType, *catalog.FieldDefinition) {
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
		r           *resources.Resource
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

func TestSharedContentTypeAttachmentConflictsAndDefaultIsolation(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	changed, e := s.AttachContentType(ctx, "bob", b.ID, beforeB.Version, AttachContentTypeInput{ContentTypeID: typ.ID, IsDefault: true})
	if e != nil {
		t.Fatal(e)
	}
	if changed.Version != beforeB.Version+1 || latest(t, s, a.ID).Version != beforeA.Version {
		t.Fatal("default updated another collection", changed)
	}
	if live, e := s.GetContentType(ctx, "bob", typ.ID); e != nil || live.Version != typ.Version {
		t.Fatal("default changed catalog definition", live, e)
	}
	count, e := s.DAL.Audit().Count(ctx, auditstore.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	if same, e := s.AttachContentType(ctx, "bob", b.ID, changed.Version, AttachContentTypeInput{ContentTypeID: typ.ID, IsDefault: true}); e != nil || same.Version != changed.Version {
		t.Fatal("attachment retry changed schema", same, e)
	}
	if n, e := s.DAL.Audit().Count(ctx, auditstore.Filter{}); e != nil || n != count {
		t.Fatal("attachment retry wrote audit", n, e)
	}
	other, e := s.CreateCatalogField(ctx, "alice", CreateField{Key: d.Key, Label: "Different code", Type: "integer"})
	if e != nil {
		t.Fatal(e)
	}
	conflict, e := s.CreateContentType(ctx, "alice", ContentTypeInput{Key: "conflict", Name: "Conflict", FieldIds: []string{other.ID}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AttachContentType(ctx, "bob", b.ID, changed.Version, AttachContentTypeInput{ContentTypeID: conflict.ID, IsDefault: true}); !isValidation(e) {
		t.Fatal("conflicting keys attached", e)
	}
	if after, e := s.Get(ctx, "bob", b.ID); e != nil || after.Version != changed.Version || *after.SchemaHeadID != *changed.SchemaHeadID {
		t.Fatal("failed attachment changed schema", after, e)
	}
	if defaultType, e := s.contentType(ctx, b.ID, ""); e != nil || defaultType.ID != typ.ID {
		t.Fatal("failed attachment changed default", defaultType, e)
	}
	if _, e = s.contentType(ctx, b.ID, conflict.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("failed attachment persisted", e)
	}
}

func TestSharedFieldRemovalChecksRemoteViewsAndRetainsHistory(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	r, e := s.Create(ctx, "bob", b.ID, CreateResource{Kind: "item", Name: "Stored", ContentTypeID: typ.ID, Values: map[string]any{"code": "A"}})
	if e != nil {
		t.Fatal(e)
	}
	rev, e := s.revision(ctx, *r.HeadRevisionID)
	if e != nil {
		t.Fatal(e)
	}
	view, e := s.CreateView(ctx, "bob", b.ID, ViewInput{Name: "Codes", Columns: []string{"code"}})
	if e != nil {
		t.Fatal(e)
	}
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteCatalogField(ctx, "alice", d.ID, d.Version); !isValidation(e) {
		t.Fatal("ignored remote view", e)
	}
	if _, e = s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, FieldIds: []string{}}); !isValidation(e) {
		t.Fatal("membership removal ignored remote view", e)
	}
	for _, before := range []*resources.Resource{beforeA, beforeB} {
		after, e := s.DAL.Resources().Resource(ctx, before.ID)
		if e != nil || after.Version != before.Version {
			t.Fatal("failed removal changed collection", after, e)
		}
	}
	if e = s.DeleteView(ctx, "bob", view.ID, view.Version); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteCatalogField(ctx, "alice", d.ID, d.Version); e != nil {
		t.Fatal(e)
	}
	for _, c := range []*resources.Resource{a, b} {
		defs, e := s.collectionFields(ctx, c.ID)
		if e != nil || len(defs) != 0 {
			t.Fatal("removed field remains live", defs, e)
		}
	}
	history, e := s.DAL.Catalog().Schema(ctx, rev.SchemaRevisionID)
	if e != nil {
		t.Fatal(e)
	}
	defs, _, e := typedDefinitions(history.Definition, typ.ID)
	if e != nil || len(defs) != 1 || defs[0].Key != d.Key {
		t.Fatal("history lost removed definition", defs, e)
	}
	fresh, e := s.CreateCatalogField(ctx, "alice", CreateField{Key: d.Key, Label: d.Label, Type: "text"})
	if e != nil {
		t.Fatal(e)
	}
	typ, e = s.GetContentType(ctx, "alice", typ.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: typ.Name, FieldIds: []string{fresh.ID}}); !isValidation(e) {
		t.Fatal("historical key reused by another catalog ID", e)
	}
}

func TestConcurrentSharedContentTypeUpdatesHaveOneAtomicWinner(t *testing.T) {
	s, a, b, typ, _ := sharedTypeFixture(t)
	ctx := context.Background()
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, name := range []string{"First proposal", "Second proposal"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := s.UpdateContentType(ctx, "alice", typ.ID, typ.Version, ContentTypeInput{Name: name, FieldIds: typ.FieldIds})
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	winners, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			winners++
		} else if errors.Is(e, ErrPreconditionFailed) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatal(winners, conflicts)
	}
	current, e := s.GetContentType(ctx, "bob", typ.ID)
	if e != nil || current.Version != typ.Version+1 {
		t.Fatal(current, e)
	}
	for _, before := range []*resources.Resource{beforeA, beforeB} {
		after, e := s.DAL.Resources().Resource(ctx, before.ID)
		if e != nil || after.Version != before.Version+1 {
			t.Fatal("fan-out was not atomic", after, e)
		}
		schema, e := s.DAL.Catalog().Schema(ctx, *after.SchemaHeadID)
		if e != nil {
			t.Fatal(e)
		}
		var definition SchemaDefinition
		if e = json.Unmarshal(schema.Definition, &definition); e != nil {
			t.Fatal(e)
		}
		for _, v := range definition.ContentTypes {
			if v.ID == typ.ID && v.Name != current.Name {
				t.Fatal("collections adopted different winners", v, current)
			}
		}
	}
}

func TestTemplateAdoptionUpdatesSharedFieldsAndRollsBackRemoteRequirements(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	if _, e := s.Create(ctx, "alice", a.ID, CreateResource{Kind: "item", Name: "Valid", ContentTypeID: typ.ID, Values: map[string]any{"code": "A"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Create(ctx, "bob", b.ID, CreateResource{Kind: "item", Name: "Empty", ContentTypeID: typ.ID}); e != nil {
		t.Fatal(e)
	}
	tpl, e := s.CreateTemplate(ctx, "alice", a.WorkspaceID, TemplateInput{Key: "labels", Name: "Labels", Fields: []CreateField{{Key: d.Key, Label: "Shared code", Type: "text"}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(ctx, "alice", a.ID, tpl.ID, latest(t, s, a.ID).Version, tpl.Version, typ.ID); e != nil {
		t.Fatal(e)
	}
	for _, c := range []*resources.Resource{a, b} {
		defs, e := s.collectionFields(ctx, c.ID)
		if e != nil || len(defs) != 1 || defs[0].ID != d.ID || defs[0].Label != "Shared code" {
			t.Fatal("template forked the shared field", defs, e)
		}
	}
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	d, e = s.DAL.Catalog().Field(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	strict, e := s.CreateTemplate(ctx, "alice", a.WorkspaceID, TemplateInput{Key: "strict", Name: "Strict", Fields: []CreateField{{Key: d.Key, Label: "Required code", Type: "text", Required: true}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(ctx, "alice", a.ID, strict.ID, beforeA.Version, strict.Version, typ.ID); !isValidation(e) {
		t.Fatal("template ignored remote missing value", e)
	}
	live, e := s.DAL.Catalog().Field(ctx, d.ID)
	if e != nil || live.Version != d.Version || live.Required || live.Label != d.Label {
		t.Fatal("template definition escaped rollback", live, e)
	}
	for _, before := range []*resources.Resource{beforeA, beforeB} {
		after, e := s.DAL.Resources().Resource(ctx, before.ID)
		if e != nil || after.Version != before.Version || *after.SchemaHeadID != *before.SchemaHeadID {
			t.Fatal("template schema escaped rollback", after, e)
		}
	}
}

func TestSharedFieldUniquenessRollsBackAllCollectionClaims(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	for _, v := range []struct {
		c             *resources.Resource
		subject, name string
	}{{a, "alice", "One"}, {b, "bob", "Two"}, {b, "bob", "Duplicate"}} {
		if _, e := s.Create(ctx, v.subject, v.c.ID, CreateResource{Kind: "item", Name: v.name, ContentTypeID: typ.ID, Values: map[string]any{"code": "A"}}); e != nil {
			t.Fatal(e)
		}
	}
	beforeA := latest(t, s, a.ID)
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	indexed := true
	opts := d.Options
	opts.Unique = true
	if _, e = s.UpdateCatalogField(ctx, "alice", d.ID, d.Version, UpdateField{Indexed: &indexed, Options: &opts}); !errors.Is(e, ErrConflict) {
		t.Fatal("remote duplicate was accepted", e)
	}
	if claims, e := keyCount(s, catalog.KeyFilter{}); e != nil || claims != 0 {
		t.Fatal("key claims escaped rollback", claims, e)
	}
	if ops, e := s.DAL.Operations().Count(ctx, operations.Filter{}); e != nil || ops != 0 {
		t.Fatal("index operations escaped rollback", ops, e)
	}
	if live, e := s.DAL.Catalog().Field(ctx, d.ID); e != nil || live.Version != d.Version || live.Options.Unique || live.Indexed {
		t.Fatal("field definition escaped rollback", live, e)
	}
	for _, before := range []*resources.Resource{beforeA, beforeB} {
		after, e := s.DAL.Resources().Resource(ctx, before.ID)
		if e != nil || after.Version != before.Version {
			t.Fatal("schema escaped rollback", after, e)
		}
	}
}

func TestSharedContentTypeSurvivesOriginWorkspaceDeletion(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	beforeB, e := s.Get(ctx, "bob", b.ID)
	if e != nil {
		t.Fatal(e)
	}
	w := latest(t, s, a.WorkspaceID)
	if e = s.Delete(ctx, "alice", w.ID, w.Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	if current, e := s.GetContentType(ctx, "bob", typ.ID); e != nil || current.ID != typ.ID {
		t.Fatal("catalog was owned by deleted workspace", current, e)
	}
	deleted, e := s.DAL.Resources().Resource(ctx, a.ID)
	if e != nil || deleted.DeletedAt == nil {
		t.Fatal(deleted, e)
	}
	label := "Retained code"
	if _, e = s.UpdateCatalogField(ctx, "alice", d.ID, d.Version, UpdateField{Label: &label}); e != nil {
		t.Fatal(e)
	}
	live, e := s.Get(ctx, "bob", b.ID)
	if e != nil || live.Version != beforeB.Version+1 {
		t.Fatal("remaining attachment stopped receiving edits", live, e)
	}
	if after, e := s.DAL.Resources().Resource(ctx, a.ID); e != nil || after.Version != deleted.Version || *after.SchemaHeadID != *deleted.SchemaHeadID {
		t.Fatal("deleted collection schema changed", after, e)
	}
	defs, e := s.Fields(ctx, "bob", b.ID)
	if e != nil || len(defs) != 1 || defs[0].ID != d.ID || defs[0].Label != label {
		t.Fatal(defs, e)
	}
}

func TestSharedContentTypesLiveFieldsDefaultsAndFrozenHistory(t *testing.T) {
	s, a, b, typ, d := sharedTypeFixture(t)
	ctx := context.Background()
	var items []*resources.Resource
	for i, v := range []struct {
		r       *resources.Resource
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
	old, e := s.revision(ctx, *items[1].PublishedRevisionID)
	if e != nil {
		t.Fatal(e)
	}
	frozen, e := s.DAL.Catalog().Schema(ctx, old.SchemaRevisionID)
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
	if e != nil || d.IndexStatus != "building" {
		t.Fatal(d, e)
	}
	for _, v := range []struct {
		r       *resources.Resource
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
		finished, e := s.DAL.Operations().Count(ctx, operations.Filter{FieldID: d.ID, Statuses: []operations.Status{operations.StatusSucceeded}})
		if e != nil {
			t.Fatal(e)
		}
		if finished == 1 {
			live, e := s.DAL.Catalog().Field(ctx, d.ID)
			if e != nil || live.IndexStatus != "building" {
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
		r       *resources.Resource
		subject string
	}{{a, "alice"}, {b, "bob"}} {
		q, e := s.Query(ctx, v.subject, v.r.ID, QueryRequest{Query: QuerySpec{ContentTypeID: typ.ID, Filter: &FilterExpr{Field: "code", Value: json.RawMessage(`"A"`)}}})
		if e != nil || len(q.Data) != 1 {
			t.Fatal(q, e)
		}
	}
	frozen, e = s.DAL.Catalog().Schema(ctx, old.SchemaRevisionID)
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
	count, e := s.DAL.Audit().Count(ctx, auditstore.Filter{})
	if e != nil {
		t.Fatal(e)
	}
	required := true
	if _, e = s.UpdateCatalogField(ctx, "alice", d.ID, d.Version, UpdateField{Required: &required}); !isValidation(e) {
		t.Fatal("invalid remote head accepted", e)
	}
	live, e := s.DAL.Catalog().Field(ctx, d.ID)
	if e != nil || live.Required || live.Version != d.Version {
		t.Fatal("field change leaked", live, e)
	}
	for _, before := range []*resources.Resource{beforeA, beforeB} {
		after, e := s.DAL.Resources().Resource(ctx, before.ID)
		if e != nil || after.Version != before.Version || *after.SchemaHeadID != *before.SchemaHeadID {
			t.Fatal("schema change leaked", after, e)
		}
	}
	if after, e := s.DAL.Audit().Count(ctx, auditstore.Filter{}); e != nil || after != count {
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
