package dms

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"papergo/internal/model"
)

func TestReclassifyItemPrunesValuesAndRestoresTypeHistory(t *testing.T) {
	s, _, c := fixture(t)
	common, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "common", Label: "Common", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "old", Label: "Old", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	required, err := s.CreateCatalogField(testContext, "alice", CreateField{Key: "new", Label: "New", Type: "text", Required: true, Options: model.FieldOptions{DefaultValue: json.RawMessage(`"creation-only"`)}})
	if err != nil {
		t.Fatal(err)
	}
	a := typeOK(t, s, c.ID, ContentTypeInput{Key: "a", Name: "A", FieldIds: []string{common.ID, old.ID}}, true)
	b := typeOK(t, s, c.ID, ContentTypeInput{Key: "b", Name: "B", FieldIds: []string{common.ID, required.ID}})
	i := create(t, s, c.ID, "item", "Item", map[string]any{"common": "keep", "old": "drop"})
	if _, err = s.Publish(testContext, "alice", i.ID, i.Version); err != nil {
		t.Fatal(err)
	}
	i = latest(t, s, i.ID)
	if _, err = s.Update(testContext, "alice", i.ID, i.Version, UpdateResource{ContentTypeID: &b.ID}); !isValidation(err) {
		t.Fatal("required field/default bypassed", err)
	}
	values := map[string]any{"new": "supplied"}
	out, err := s.Update(testContext, "alice", i.ID, i.Version, UpdateResource{ContentTypeID: &b.ID, Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	if *out.ContentTypeID != b.ID || out.Values["common"] != "keep" || out.Values["new"] != "supplied" || out.Values["old"] != nil {
		t.Fatal("reclassification", out)
	}
	pub, err := s.GetSurface(testContext, "alice", i.ID, "published")
	if err != nil || *pub.ContentTypeID != a.ID || pub.Values["old"] != "drop" {
		t.Fatal("publication changed", pub, err)
	}
	v, err := s.Revision(testContext, "alice", i.ID, 2)
	if err != nil || !slices.Contains(v.ChangedFields, "$content_type_id") {
		t.Fatal("type change history", v, err)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, a.ID, latest(t, s, c.ID).Version); err != nil {
		t.Fatal("historical type prevented detach", err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", i.ID, 1, out.Version); !isValidation(err) {
		t.Fatal("detached historical type restored", err)
	}
	if _, err = s.AttachContentType(testContext, "alice", c.ID, latest(t, s, c.ID).Version, AttachContentTypeInput{ContentTypeID: *a.ParentID}); err != nil {
		t.Fatal(err)
	}
	restored, err := s.RestoreRevision(testContext, "alice", i.ID, 1, out.Version)
	if err != nil || *restored.ContentTypeID != a.ID || restored.Values["old"] != "drop" || restored.Values["new"] != nil {
		t.Fatal("historical type restore", restored, err)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, a.ID, latest(t, s, c.ID).Version); !errors.Is(err, ErrConflict) {
		t.Fatal("active type detached", err)
	}
}

func TestDetachContentTypeDefaultAndDeletedRecovery(t *testing.T) {
	s, _, c := fixture(t)
	types, err := s.CollectionContentTypes(testContext, "alice", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	original := types[0]
	if _, err = s.DetachContentType(testContext, "alice", c.ID, original.ID, c.Version); !errors.Is(err, ErrConflict) {
		t.Fatal("last type detached", err)
	}
	i := create(t, s, c.ID, "item", "Deleted", nil)
	b := typeOK(t, s, c.ID, ContentTypeInput{Key: "b", Name: "B"})
	if err = s.Delete(testContext, "alice", i.ID, i.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, original.ID, c.Version); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale detach", err)
	}
	if _, err = s.DetachContentType(testContext, "alice", c.ID, original.ID, latest(t, s, c.ID).Version); err != nil {
		t.Fatal(err)
	}
	newItem := create(t, s, c.ID, "item", "New default", nil)
	if *newItem.ContentTypeID != b.ID {
		t.Fatal("default not replaced", newItem)
	}
	if _, err = s.GetContentType(testContext, "alice", *original.ParentID); err != nil {
		t.Fatal("catalog type deleted", err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", c.ID, i.ID, false); !isValidation(err) {
		t.Fatal("unattached deleted type restored", err)
	}
	if _, err = s.AttachContentType(testContext, "alice", c.ID, latest(t, s, c.ID).Version, AttachContentTypeInput{ContentTypeID: *original.ParentID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", c.ID, i.ID, false); err != nil {
		t.Fatal("reattached type cannot restore", err)
	}
}

func TestDetachContentTypeSavedViewRollsBack(t *testing.T) {
	s, _, c := fixture(t)
	a := typeOK(t, s, c.ID, ContentTypeInput{Key: "a", Name: "A"}, true)
	if _, err := s.CreateView(testContext, "alice", c.ID, ViewInput{Name: "A only", Query: QuerySpec{ContentTypeID: a.ID}}); err != nil {
		t.Fatal(err)
	}
	version := latest(t, s, c.ID).Version
	if _, err := s.DetachContentType(testContext, "alice", c.ID, a.ID, version); !isValidation(err) {
		t.Fatal("dependent view accepted", err)
	}
	if current := latest(t, s, c.ID); current.Version != version {
		t.Fatal("failed detach changed schema", current)
	}
	if typ, err := s.contentType(testContext, c.ID, ""); err != nil || typ.ID != a.ID {
		t.Fatal("default attachment not rolled back", typ, err)
	}
}
