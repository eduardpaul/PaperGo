package dms

import (
	"errors"
	"papergo/internal/dal/directory"
	"papergo/internal/identity"
	"slices"
	"testing"
)

func TestRevisionHistoryChangesPaginationAndRestore(t *testing.T) {
	s, workspace, list := fixture(t)
	fieldOK(t, s, list.ID, CreateField{Key: "a", Label: "A", Type: "text"})
	fieldOK(t, s, list.ID, CreateField{Key: "b", Label: "B", Type: "text"})
	item := create(t, s, list.ID, "item", "Original", map[string]any{"a": "old"})
	values := map[string]any{"a": "new", "b": "remove on restore"}
	item, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Changed"), Keywords: &[]string{"new"}, Values: &values})
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 1)
	if err != nil || len(history.Data) != 1 || !history.Data[0].IsCurrent || !slices.Equal(history.Data[0].ChangedFields, []string{"$keywords", "$name", "a", "b"}) {
		t.Fatal(history, err)
	}
	if history.Data[0].SchemaRevision == nil {
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
	if revision, err := s.Revision(testContext, "reader", item.ID, 1); err != nil || revision.Name != "Original" {
		t.Fatal("ordinary reader cannot read history", revision, err)
	}
	if _, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version-1); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale restore", err)
	}
	restored, err := s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version)
	if err != nil || restored.Name != "Original" || len(restored.Keywords) != 0 || restored.Values["a"] != "old" {
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

func TestOrdinaryReaderHistorySnapshotsAndAuthorization(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	field := fieldOK(t, s, l.ID, CreateField{Key: "note", Label: "Original label", Type: "text"})
	item := create(t, s, l.ID, "item", "First", map[string]any{"note": "first"})
	publishItem(t, s, item)
	item = latest(t, s, item.ID)
	item, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Name: ptr("Unpublished"), Values: &map[string]any{"note": "draft"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateField(testContext, "alice", l.ID, field.ID, latest(t, s, l.ID).Version, UpdateField{Label: ptr("Current label")}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RevisionHistory(testContext, "reader", item.ID, 0, 1)
	if err != nil || len(rows.Data) != 1 || !rows.Data[0].IsCurrent || rows.Data[0].Name != "Unpublished" || rows.NextCursor != "2" || !slices.Equal(rows.Data[0].ChangedFields, []string{"$name", "note"}) {
		t.Fatal(rows, err)
	}
	defs, _, err := typedDefinitions(rows.Data[0].SchemaRevision.Definition, rows.Data[0].ContentTypeID)
	if err != nil || definitionMap(defs)["note"].Label != "Original label" {
		t.Fatal("original schema lost", defs, err)
	}
	rows, err = s.RevisionHistory(testContext, "reader", item.ID, 2, 1)
	if err != nil || len(rows.Data) != 1 || rows.Data[0].IsCurrent || rows.Data[0].Name != "First" || rows.NextCursor != "" {
		t.Fatal(rows, err)
	}
	if current, err := s.Get(testContext, "reader", item.ID); err != nil || current.Name != "First" {
		t.Fatal("current content visibility changed", current, err)
	}
	if _, err = s.GetSurface(testContext, "reader", item.ID, "head"); !errors.Is(err, ErrForbidden) {
		t.Fatal("head permission changed", err)
	}
	if _, err = s.RestoreRevision(testContext, "reader", item.ID, 1, item.Version); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader restored history", err)
	}
	private := create(t, s, l.ID, "item", "Private", nil)
	private, err = s.SetPermissions(testContext, "alice", private.ID, private.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, get := range []func() error{
		func() error { _, e := s.RevisionHistory(testContext, "reader", private.ID, 0, 10); return e },
		func() error { _, e := s.Revision(testContext, "reader", private.ID, 1); return e },
	} {
		if e := get(); !errors.Is(e, ErrForbidden) {
			t.Fatal("private history exposed", e)
		}
	}
	if err = s.Directory().SetAssignment(testContext, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "reader"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Revision(testContext, "reader", item.ID, 1); !errors.Is(err, ErrForbidden) {
		t.Fatal("module scope bypassed", err)
	}
	if _, err = s.RevisionHistory(testContext, "reader", item.ID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatal("history list bypassed module scope", err)
	}
	if err = s.Directory().SetAssignment(testContext, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "reader"}, false); err != nil {
		t.Fatal(err)
	}
	item, err = s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevisionHistory(testContext, "reader", item.ID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked permission retained", err)
	}
	if err = s.Delete(testContext, "alice", item.ID, item.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Revision(testContext, "alice", item.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted history exposed", err)
	}
	if _, err = s.RevisionHistory(testContext, "alice", item.ID, 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted history list exposed", err)
	}
}

func TestRestoreRevisionValidatesCurrentSchemaAndKeepsBlob(t *testing.T) {
	s, workspace, _ := fixture(t)
	library := davLibrary(t, s, workspace, false)
	if _, err := s.Update(testContext, "alice", library.ID, library.Version, UpdateResource{Versioning: ptr("major")}); err != nil {
		t.Fatal(err)
	}
	f := fieldOK(t, s, library.ID, CreateField{Key: "note", Label: "Note", Type: "text"})
	item := create(t, s, library.ID, "item", "File.txt", map[string]any{"note": "long original value"})
	item, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"note": "short"}})
	if err != nil {
		t.Fatal(err)
	}
	// A later policy tightening rejects values restored from old history.
	blob, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, pdfContent("File.txt"))
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
