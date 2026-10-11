package dms

import (
	"errors"
	"papergo/ent/resource"
	"slices"
	"testing"
)

func TestHistoryDefaultsAndPolicyValidation(t *testing.T) {
	s, w, _ := fixture(t)
	for _, tc := range []struct{ kind, mode string }{{"list", "off"}, {"library", "major"}} {
		c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: tc.kind, Name: tc.kind})
		if err != nil || string(c.Versioning) != tc.mode || c.MaxVersions != 50 {
			t.Fatal(c, err)
		}
		item := create(t, s, c.ID, "item", "Item", nil)
		rows, err := s.Revisions(testContext, "alice", item.ID, 0, 10)
		expected := 0
		if tc.mode == "major" {
			expected = 1
		}
		if err != nil || len(rows.Data) != expected {
			t.Fatal(rows, err)
		}
		if _, err = s.ItemSchema(testContext, "alice", item.ID, "head"); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []CreateResource{
		{Kind: "list", Name: "Invalid", Versioning: ptr("minor")},
		{Kind: "list", Name: "Invalid", MaxVersions: ptr(0)},
		{Kind: "list", Name: "Invalid", MaxVersions: ptr(501)},
		{Kind: "folder", Name: "Invalid", Versioning: ptr("major")},
	} {
		if _, err := s.Create(testContext, "alice", w.ID, in); err == nil {
			t.Fatal("invalid history policy accepted", in)
		}
	}
	templates, err := s.CollectionTemplates(testContext, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range templates {
		c, err := s.Create(testContext, "alice", w.ID, CreateResource{TemplateKey: recipe.Key, Name: recipe.Name})
		if err != nil || string(c.Versioning) != recipe.Versioning || c.MaxVersions != recipe.MaxVersions {
			t.Fatal(c, err)
		}
	}
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{TemplateKey: "documents", Name: "No history", Versioning: ptr("off"), MaxVersions: ptr(500)})
	if err != nil || c.Versioning != resource.VersioningOff || c.MaxVersions != 500 {
		t.Fatal(c, err)
	}
}

func TestHistoryRetentionDisableResumeAndPublishedSnapshot(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "History", Versioning: ptr("major"), MaxVersions: ptr(2), PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, c.ID, CreateField{Key: "unchanged", Label: "Unchanged", Type: "text"})
	other := create(t, s, c.ID, "item", "Other", nil)
	item := create(t, s, c.ID, "item", "First", map[string]any{"unchanged": "value"})
	publishItem(t, s, item)
	item = latest(t, s, item.ID)
	for _, name := range []string{"Second", "Third"} {
		item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 2 || rows.Data[0].RevisionNumber != 2 || !slices.Equal(rows.Data[0].ChangedFields, []string{"$name"}) {
		t.Fatal(rows, err)
	}
	if otherRows, e := s.Revisions(testContext, "alice", other.ID, 0, 10); e != nil || len(otherRows.Data) != 1 {
		t.Fatal("retention affected another item", otherRows, e)
	}
	for _, fetch := range []func() error{
		func() error { _, e := s.Revision(testContext, "alice", item.ID, 1); return e },
		func() error { _, e := s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version); return e },
	} {
		if e := fetch(); !errors.Is(e, ErrNotFound) {
			t.Fatal("trimmed revision accessible", e)
		}
	}
	c, err = s.Update(testContext, "alice", c.ID, latest(t, s, c.ID).Version, UpdateResource{Versioning: ptr("off"), MaxVersions: ptr(1)})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("History off")})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 2 || rows.Data[1].IsCurrent {
		t.Fatal(rows, err)
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 4); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled snapshot exposed", err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 4, item.Version); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled snapshot restored", err)
	}
	if head := latest(t, s, item.ID); head.Name != "History off" {
		t.Fatal(head)
	}
	if published, e := s.GetSurface(testContext, "alice", item.ID, "published"); e != nil || published.Name != "First" {
		t.Fatal(published, e)
	}
	// Historical changes remain readable and restorable while history is off.
	item, err = s.RestoreRevision(testContext, "alice", item.ID, 2, item.Version)
	if err != nil || item.Name != "Second" {
		t.Fatal(item, err)
	}
	c, err = s.Update(testContext, "alice", c.ID, c.Version, UpdateResource{Versioning: ptr("major")})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Resumed")})
	if err != nil {
		t.Fatal(err)
	}
	rows, err = s.RevisionHistory(testContext, "alice", item.ID, 0, 10)
	if err != nil || len(rows.Data) != 1 || rows.Data[0].Name != "Resumed" || !rows.Data[0].IsCurrent {
		t.Fatal(rows, err)
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestHistoryDisabledKeepsEventAndPublishedSnapshots(t *testing.T) {
	s, w, _ := fixture(t)
	c, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Drafts", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item := create(t, s, c.ID, "item", "First", nil)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Second")})
	if err != nil || revisionCount(t, s, item.ID) != 2 {
		t.Fatal(item, err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Third")})
	if err != nil || revisionCount(t, s, item.ID) != 3 {
		t.Fatal(item, err)
	}
	if _, err = s.Unpublish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
}
