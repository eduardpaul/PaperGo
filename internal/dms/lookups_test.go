package dms

import (
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/internal/model"
	"testing"
)

func TestCrossWorkspaceLookupDefaultsSharingAndTargetValidation(t *testing.T) {
	s, w, source := fixture(t)
	remote, e := s.Create(testContext, "bob", "", CreateResource{Kind: "workspace", Name: "Private catalog"})
	if e != nil {
		t.Fatal(e)
	}
	target, e := s.Create(testContext, "bob", remote.ID, CreateResource{Kind: "list", Name: "Contacts", PublishingEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	contact, e := s.Create(testContext, "bob", target.ID, CreateResource{Kind: "item", Name: "Private draft"})
	if e != nil {
		t.Fatal(e)
	}
	folder, e := s.Create(testContext, "bob", target.ID, CreateResource{Kind: "folder", Name: "Group"})
	if e != nil {
		t.Fatal(e)
	}
	defaults, _ := json.Marshal(contact.ID)
	d, e := s.CreateCatalogField(testContext, "alice", CreateField{Key: "contact", Label: "Contact", Type: "lookup", Options: model.FieldOptions{LookupContainerID: target.ID, DefaultValue: defaults}})
	if e != nil {
		t.Fatal("inaccessible cross-workspace catalog/default", e)
	}
	typ, e := s.CreateContentType(testContext, "alice", ContentTypeInput{Key: "reference", Name: "Reference", FieldIds: []string{d.ID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		collection *ent.Resource
		actor      string
	}{{source, "alice"}, {target, "bob"}} {
		if _, e = s.AttachContentType(testContext, c.actor, c.collection.ID, c.collection.Version, AttachContentTypeInput{ContentTypeID: typ.ID, IsDefault: true}); e != nil {
			t.Fatal(e)
		}
	}
	r := create(t, s, source.ID, "item", "Reference", nil)
	if r.Values["contact"] != contact.ID {
		t.Fatal("default was not applied", r.Values)
	}
	for _, id := range []string{target.ID, contact.ID} {
		if _, e = s.Get(testContext, "alice", id); !errors.Is(e, ErrForbidden) {
			t.Fatal("lookup granted target access", e)
		}
	}
	other := create(t, s, w.ID, "list", "Wrong collection", nil)
	wrong := create(t, s, other.ID, "item", "Wrong item", nil)
	for _, id := range []string{folder.ID, target.ID, wrong.ID, "00000000-0000-4000-8000-000000000001"} {
		if _, e = s.Create(testContext, "alice", source.ID, CreateResource{Kind: "item", Name: "Invalid", Values: map[string]any{"contact": id}}); e == nil {
			t.Fatal("accepted invalid lookup", id)
		}
	}
	// Template defaults use the same existence validation and can be adopted.
	tpl, e := s.CreateTemplate(testContext, "alice", w.ID, TemplateInput{Key: "refs", Name: "References", Fields: []CreateField{{Key: "ref", Label: "Reference", Type: "lookup", Options: model.FieldOptions{LookupContainerID: target.ID, Multiple: true, DefaultValue: json.RawMessage(`[` + string(defaults) + `]`)}}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyTemplate(testContext, "alice", other.ID, tpl.ID, latest(t, s, other.ID).Version, tpl.Version, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(testContext, "bob", contact.ID, contact.Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(testContext, "alice", source.ID, CreateResource{Kind: "item", Name: "Deleted target"}); e == nil {
		t.Fatal("accepted deleted default target")
	}
	name := "Retained reference"
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &name}); e != nil {
		t.Fatal("unchanged reference invalidated", e)
	}
	assigned := map[string]any{"contact": contact.ID}
	current := latest(t, s, r.ID)
	if _, e = s.Update(testContext, "alice", r.ID, current.Version, UpdateResource{Values: &assigned}); e == nil {
		t.Fatal("explicit deleted reference was retained")
	}
	if latest(t, s, r.ID).Version != current.Version {
		t.Fatal("failed lookup assignment consumed a version")
	}
	if e = s.Delete(testContext, "bob", target.ID, latestForActor(t, s, "bob", target.ID).Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	for _, id := range []string{target.ID, folder.ID, remote.ID, "missing"} {
		if _, e = s.CreateCatalogField(testContext, "alice", CreateField{Key: "invalid", Label: "Invalid", Type: "lookup", Options: model.FieldOptions{LookupContainerID: id}}); e == nil {
			t.Fatal("accepted invalid lookup collection", id)
		}
	}
}

func TestCrossWorkspaceLookupExplicitMultipleAssignmentRevalidatesEveryTarget(t *testing.T) {
	s, _, source := fixture(t)
	w, e := s.Create(testContext, "bob", "", CreateResource{Kind: "workspace", Name: "Catalog"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Create(testContext, "bob", w.ID, CreateResource{Kind: "library", Name: "Contacts", PublishingEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Create(testContext, "bob", c.ID, CreateResource{Kind: "item", Name: "A"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(testContext, "bob", c.ID, CreateResource{Kind: "item", Name: "B"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateField(testContext, "alice", source.ID, CreateField{Key: "contacts", Label: "Contacts", Type: "lookup", Options: model.FieldOptions{LookupContainerID: c.ID, Multiple: true}}); e != nil {
		t.Fatal(e)
	}
	r := create(t, s, source.ID, "item", "References", map[string]any{"contacts": []any{a.ID, b.ID}})
	assigned := map[string]any{"contacts": []any{a.ID, b.ID}}
	r, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &assigned})
	if e != nil {
		t.Fatal("live repeated assignment", e)
	}
	if e = s.Delete(testContext, "bob", a.ID, a.Version, DeleteOptions{}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &assigned}); e == nil {
		t.Fatal("accepted array containing deleted retained target")
	}
	name := "Omitted references"
	r, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &name})
	if e != nil {
		t.Fatal(e)
	}
	assigned = map[string]any{"contacts": []any{b.ID}}
	if _, e = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Values: &assigned}); e != nil {
		t.Fatal("cannot remove dead reference", e)
	}
}

func latestForActor(t *testing.T, s *Service, actor, id string) *ent.Resource {
	t.Helper()
	r, e := s.Get(testContext, actor, id)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
