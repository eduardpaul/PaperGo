package dms

import (
	"errors"
	"slices"
	"testing"
)

func TestRevisionHistoryChangesPaginationAndRestore(t *testing.T) {
	s, workspace, list := fixture(t)
	fieldOK(t, s, list.ID, CreateField{Key: "a", Label: "A", Type: "text"})
	fieldOK(t, s, list.ID, CreateField{Key: "b", Label: "B", Type: "text"})
	item := create(t, s, list.ID, "item", "Original", map[string]any{"a": "old"})
	values := map[string]any{"a": "new", "b": "remove on restore"}
	item, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Changed"), Tags: &[]string{"new"}, Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.RevisionHistory(testContext, "alice", item.ID, 1, 10)
	if err != nil || len(history) != 1 || !history[0].IsCurrent || !slices.Equal(history[0].ChangedFields, []string{"$name", "$tags", "a", "b"}) {
		t.Fatal(history, err)
	}
	if history[0].Edges.SchemaRevision == nil {
		t.Fatal("original schema missing")
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 3); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing revision", err)
	}
	other := create(t, s, list.ID, "item", "Other", nil)
	if _, err = s.Revision(testContext, "alice", other.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatal("revision leaked across items", err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", workspace.ID, WorkspaceMember{"reader", "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Revision(testContext, "reader", item.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatal("draft history leaked", err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version-1); !errors.Is(err, ErrConflict) {
		t.Fatal("stale restore", err)
	}
	restored, err := s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version)
	if err != nil || restored.Name != "Original" || len(restored.Tags) != 0 || restored.Values["a"] != "old" {
		t.Fatal(restored, err)
	}
	if _, ok := restored.Values["b"]; ok {
		t.Fatal("field absent from snapshot retained", restored.Values)
	}
	first, err := s.Revision(testContext, "alice", item.ID, 1)
	if err != nil || first.IsCurrent || first.Name != "Original" {
		t.Fatal("old revision changed", first, err)
	}
	current, err := s.Revision(testContext, "alice", item.ID, 3)
	if err != nil || !current.IsCurrent || current.CreatedBy != "alice" {
		t.Fatal(current, err)
	}
}

func TestRestoreRevisionValidatesCurrentSchemaAndKeepsBlob(t *testing.T) {
	s, workspace, _ := fixture(t)
	library := davLibrary(t, s, workspace, false)
	f := fieldOK(t, s, library.ID, CreateField{Key: "note", Label: "Note", Type: "text"})
	item := create(t, s, library.ID, "item", "File.txt", map[string]any{"note": "long original value"})
	item, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"note": "short"}})
	if err != nil {
		t.Fatal(err)
	}
	// A later policy tightening rejects values restored from old history.
	blob, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, content("File.txt"))
	if err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	item, err = s.RestoreRevision(testContext, "alice", item.ID, 2, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	restoredRevision, err := s.Revision(testContext, "alice", item.ID, 4)
	if err != nil || restoredRevision.BlobID == nil || *restoredRevision.BlobID != blob.ID {
		t.Fatal("metadata restore replaced current blob", restoredRevision, err)
	}
	options := f.Options
	options.MaxLength = ptr(5)
	if _, err = s.UpdateField(testContext, "alice", library.ID, f.ID, latest(t, s, library.ID).Version, UpdateField{Options: &options}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version); err == nil {
		t.Fatal("restored invalid historical value")
	}
	if latest(t, s, item.ID).Version != item.Version || revisionCount(t, s, item.ID) != 4 {
		t.Fatal("failed restore committed")
	}
}
