package dms

import (
	"errors"
	"github.com/google/uuid"
	"maps"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"strings"
	"testing"
)

func davLibrary(t *testing.T, s *Service, w *resources.Resource, publishing bool) *resources.Resource {
	t.Helper()
	lib, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "library", Name: "Shared", PublishingEnabled: publishing, WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return lib
}
func pdfContent(name string) BlobInput {
	return BlobInput{ObjectKey: uuid.NewString(), Filename: name, ContentType: "application/pdf", Size: 1, SHA256: strings.Repeat("a", 64)}
}
func revisionCount(t *testing.T, s *Service, id string) int {
	t.Helper()
	n, err := countRevisions(s, id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLibraryEntriesHaveUniqueFileNames(t *testing.T) {
	s, w, list := fixture(t)
	lib := davLibrary(t, s, w, false)
	report := create(t, s, lib.ID, "item", "Report.pdf", nil)
	if _, err := s.Create(testContext, "alice", lib.ID, CreateResource{Kind: "folder", Name: "report.PDF"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate accepted: %v", err)
	}
	var validation *ValidationError
	for _, name := range []string{"a/b.pdf", "a\tb.pdf", "x\x01"} {
		if _, err := s.Create(testContext, "alice", lib.ID, CreateResource{Kind: "item", Name: name}); !errors.As(err, &validation) {
			t.Fatalf("library name %q accepted: %v", name, err)
		}
	}
	// Lists hold records, not files: duplicate names stay legal there.
	create(t, s, list.ID, "item", "Same", nil)
	create(t, s, list.ID, "item", "Same", nil)
	folder := create(t, s, lib.ID, "folder", "Archive", nil)
	create(t, s, folder.ID, "item", "Report.pdf", nil)
	other := create(t, s, lib.ID, "item", "Other.pdf", nil)
	if _, err := s.Update(testContext, "alice", other.ID, other.Version, UpdateResource{Name: ptr("REPORT.pdf")}); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename onto a sibling accepted: %v", err)
	}
	if err := s.Delete(testContext, "alice", report.ID, report.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(testContext, "alice", other.ID, other.Version, UpdateResource{Name: ptr("REPORT.pdf")}); err != nil {
		t.Fatalf("deleted entry still holds its name: %v", err)
	}
}

func TestWebDAVFlagBelongsToManagedLibraries(t *testing.T) {
	s, w, list := fixture(t)
	if _, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Tasks", WebDAVEnabled: true}); err == nil {
		t.Fatal("webdav enabled on a list")
	}
	if _, err := s.Update(testContext, "alice", list.ID, list.Version, UpdateResource{WebDAVEnabled: ptr(true)}); err == nil {
		t.Fatal("webdav enabled on a list by update")
	}
	lib := create(t, s, w.ID, "library", "Docs", nil)
	if _, err := s.LibraryFile(testContext, "alice", lib.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("library served without webdav: %v", err)
	}
	readers(t, s, current(t, s, w.ID))
	if _, err := s.Update(testContext, "editor", lib.ID, lib.Version, UpdateResource{WebDAVEnabled: ptr(true)}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("writer enabled webdav: %v", err)
	}
	lib, err := s.Update(testContext, "alice", lib.ID, lib.Version, UpdateResource{WebDAVEnabled: ptr(true)})
	if err != nil || !lib.WebdavEnabled {
		t.Fatal(lib, err)
	}
	if f, err := s.LibraryFile(testContext, "reader", lib.ID, nil); err != nil || !f.Collection() {
		t.Fatal(f, err)
	}
}

func TestDeleteFolderTreeRequiresExplicitIntentAndPermissions(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	if _, err := s.CreateField(testContext, "alice", lib.ID, CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true}); err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, lib.ID, "folder", "Projects", nil)
	nested := create(t, s, folder.ID, "folder", "Nested", nil)
	inner := create(t, s, nested.ID, "item", "Plan.txt", map[string]any{"status": "active"})
	outside := create(t, s, lib.ID, "item", "Index.txt", nil)
	typ := referencesType(t, s, w.ID)
	if _, err := s.Link(testContext, "alice", outside.ID, CreateRelationship{Type: typ.ID, TargetID: inner.ID}); err != nil {
		t.Fatal(err)
	}
	readers(t, s, current(t, s, w.ID))
	// Ordinary deletion rejects a populated folder, even with an unreadable child.
	locked := current(t, s, inner.ID)
	locked, err := s.SetPermissions(testContext, "alice", inner.ID, locked.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(testContext, "editor", inner.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("child should be hidden from editor: %v", err)
	}
	if err := s.Delete(testContext, "editor", folder.ID, folder.Version, DeleteOptions{}); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("editor deleted populated folder: %v", err)
	}
	if err := s.Delete(testContext, "editor", folder.ID, folder.Version, DeleteOptions{Recursive: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor recursively deleted protected content: %v", err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version+1, DeleteOptions{}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale delete accepted: %v", err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version+1, DeleteOptions{Recursive: true}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale recursive delete accepted: %v", err)
	}
	if err := s.Delete(testContext, "editor", lib.ID, current(t, s, lib.ID).Version, DeleteOptions{}); !errors.Is(err, ErrForbidden) {
		t.Fatal("editor deleted library", err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{}); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("owner deleted populated folder: %v", err)
	}
	versions := map[string]int{folder.ID: folder.Version, nested.ID: nested.Version, inner.ID: locked.Version}
	for id, version := range versions {
		raw, err := s.DAL.Resources().Resource(testContext, id)
		if err != nil || raw.DeletedAt != nil || raw.Version != version {
			t.Fatalf("rejected delete changed resource: %+v %v", raw, err)
		}
	}
	if n, err := s.DAL.Relationships().Count(testContext, relationships.Filter{TargetID: inner.ID}); err != nil || n != 1 {
		t.Fatalf("rejected delete removed relationship: %d %v", n, err)
	}
	if n, err := s.DAL.Audit().Count(testContext, auditstore.Filter{ResourceID: folder.ID, Action: "resource.delete"}); err != nil || n != 0 {
		t.Fatalf("rejected delete was audited: %d %v", n, err)
	}
	counts := storedCounts(t, s)
	// Fail after projections have been removed to exercise
	// transaction rollback, including queued deletion events.
	stop := daltest.Reject(t, s.DAL, "resources", daltest.OnUpdate, daltest.Row{"kind": "item", "deleted_at": daltest.NotNull})
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err == nil {
		t.Fatal("injected failure did not reject recursive deletion")
	}
	if after := storedCounts(t, s); !maps.Equal(after, counts) {
		t.Fatalf("recursive rollback changed stored records: %v, want %v", after, counts)
	}
	for id, version := range versions {
		raw, err := s.DAL.Resources().Resource(testContext, id)
		if err != nil || raw.DeletedAt != nil || raw.Version != version {
			t.Fatalf("recursive rollback changed resource: %+v %v", raw, err)
		}
	}
	stop()
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	for id := range versions {
		if _, err := s.Get(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted resource readable: %v", err)
		}
		if _, err := s.RevisionHistory(testContext, "alice", id, 0, 10); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted history readable: %v", err)
		}
	}
	indexSearch(t, s)
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: "Plan"})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("deleted item searchable: %+v %v", page, err)
	}
	page, err = s.Browse(testContext, "alice", Browse{ParentID: lib.ID})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != outside.ID {
		t.Fatalf("deleted folder listed: %+v %v", page, err)
	}
	if n := revisionCount(t, s, inner.ID); n != 1 {
		t.Fatalf("revisions not retained: %d", n)
	}
	if n, err := s.DAL.Relationships().Count(testContext, relationships.Filter{TargetID: inner.ID}); err != nil || n != 1 {
		t.Fatalf("relationship not retained for recovery: %d %v", n, err)
	}
	if links, err := s.Relationships(testContext, "alice", outside.ID, "both", "", "", 50); err != nil || len(links.Data) != 0 {
		t.Fatalf("deleted relationship exposed: %+v %v", links, err)
	}
	events, err := s.DAL.Audit().List(testContext, auditstore.Filter{ResourceID: folder.ID, Action: "resource.delete"})
	if err != nil || len(events) != 1 || events[0].Details["descendants"] != float64(2) {
		t.Fatalf("delete not audited: %+v %v", events, err)
	}
	if deletedEvents, err := s.DAL.Events().Count(testContext, eventstore.Filter{Types: []string{EventItemDeleted}, ResourceID: inner.ID}); err != nil || deletedEvents != 1 {
		t.Fatalf("recursive item deletion event: %d %v", deletedEvents, err)
	}
	// Restoring below a deleted parent cannot bypass the restore transition.
	if _, err = s.DAL.Resources().Restore(testContext, inner.ID, folder.ID, "inner", "alice"); err == nil {
		t.Fatal("restored beneath a deleted parent")
	}
	if _, err = s.Create(testContext, "alice", folder.ID, CreateResource{Kind: "item", Name: "Late.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("created inside a deleted folder: %v", err)
	}
}

func TestMoveStaysInCollectionAndFollowsTheNewScope(t *testing.T) {
	s, w, list := fixture(t)
	lib := davLibrary(t, s, w, false)
	private := create(t, s, lib.ID, "folder", "Private", nil)
	nested := create(t, s, private.ID, "folder", "Nested", nil)
	item := create(t, s, lib.ID, "item", "Notes.txt", nil)
	if _, err := s.SetPermissions(testContext, "alice", private.ID, private.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}, {"user", "bob", "read", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bob reads the library item: %v", err)
	}
	revisions := revisionCount(t, s, item.ID)
	moved, err := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{ParentID: &nested.ID})
	if err != nil || *moved.ParentID != nested.ID || moved.Version != item.Version+1 {
		t.Fatal(moved, err)
	}
	if revisionCount(t, s, item.ID) != revisions {
		t.Fatal("a move created a content revision")
	}
	if r, err := s.Get(testContext, "bob", item.ID); err != nil || r.Name != "Notes.txt" {
		t.Fatalf("moved item did not join the folder scope: %v", err)
	}
	if _, err = s.Update(testContext, "alice", private.ID, current(t, s, private.ID).Version, UpdateResource{ParentID: &nested.ID}); err == nil {
		t.Fatal("folder moved into its own descendant")
	}
	if _, err = s.Update(testContext, "alice", item.ID, moved.Version, UpdateResource{ParentID: &list.ID}); err == nil {
		t.Fatal("item moved to another collection")
	}
	if err = s.DAL.Resources().Change(testContext, item.ID, moved.Version, "alice", resources.Changes{ParentID: &list.ID}); err == nil {
		t.Fatal("DAL allowed a cross-collection parent")
	}
	if err = s.DAL.Resources().Change(testContext, private.ID, current(t, s, private.ID).Version, "alice", resources.Changes{ParentID: &nested.ID}); err == nil {
		t.Fatal("DAL allowed a cycle")
	}
	// Moving the item back out drops the folder's scope again.
	if _, err = s.Update(testContext, "alice", item.ID, moved.Version, UpdateResource{ParentID: &lib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("scope kept after moving out: %v", err)
	}
	scope, err := s.DAL.Resources().One(testContext, resources.Filter{IDs: []string{item.ID}})
	if err != nil || *scope.ScopeID != w.ID {
		t.Fatal("scope not recomputed", err)
	}
}

func TestLibraryFilesPutMoveAndDelete(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	if _, err := s.MakeFolder(testContext, "alice", lib.ID, []string{"Docs"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MakeFolder(testContext, "alice", lib.ID, []string{"docs"}); !errors.Is(err, ErrExists) {
		t.Fatalf("folder created twice: %v", err)
	}
	if _, err := s.MakeFolder(testContext, "alice", lib.ID, []string{"Missing", "Child"}); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := s.MakeFolder(testContext, "alice", lib.ID, []string{"Docs", "Nested"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFile(testContext, "alice", lib.ID, []string{"Docs"}, FileConditions{IfMatch: `"stale"`}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale tree delete accepted: %v", err)
	}
	if err := s.DeleteFile(testContext, "alice", lib.ID, []string{"Docs"}, FileConditions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LibraryFile(testContext, "alice", lib.ID, []string{"Docs", "Nested"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("nested folder survived deletion: %v", err)
	}
	if _, err := s.MakeFolder(testContext, "alice", lib.ID, []string{"Docs"}); err != nil {
		t.Fatal(err)
	}
	f, created, err := s.PutFile(testContext, "alice", lib.ID, []string{"docs", "a.txt"}, PutFile{Blob: pdfContent("a.txt")})
	if err != nil || !created || f.Blob == nil || f.Name != "a.txt" {
		t.Fatal(f, created, err)
	}
	// The first revision already carries the content.
	if n := revisionCount(t, s, f.ID); n != 1 {
		t.Fatalf("new file has %d revisions", n)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "A.TXT"}, PutFile{FileConditions: FileConditions{IfNoneMatch: "*"}, Blob: pdfContent("a.txt")}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("If-None-Match ignored: %v", err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, PutFile{FileConditions: FileConditions{IfMatch: `"stale"`}, Blob: pdfContent("a.txt")}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("If-Match ignored: %v", err)
	}
	f, created, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, PutFile{FileConditions: FileConditions{IfMatch: f.ETag()}, Blob: pdfContent("a.txt")})
	if err != nil || created || revisionCount(t, s, f.ID) != 2 {
		t.Fatal(f, created, err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs"}, PutFile{Blob: pdfContent("Docs")}); !errors.Is(err, ErrExists) {
		t.Fatalf("file written over a folder: %v", err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt", "x"}, PutFile{Blob: pdfContent("x")}); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("file used as folder: %v", err)
	}
	other, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"b.txt"}, PutFile{Blob: pdfContent("b.txt")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MoveFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, []string{"B.txt"}, false); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("move overwrote without permission: %v", err)
	}
	created, err = s.MoveFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, []string{"B.txt"}, true)
	if err != nil || created {
		t.Fatal(created, err)
	}
	if _, err = s.Get(testContext, "alice", other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("overwritten file survived: %v", err)
	}
	moved, err := s.LibraryFile(testContext, "alice", lib.ID, []string{"b.TXT"})
	if err != nil || moved.ID != f.ID || moved.Name != "B.txt" {
		t.Fatal(moved, err)
	}
	// A case-only rename names the source itself.
	if created, err = s.MoveFile(testContext, "alice", lib.ID, []string{"B.txt"}, []string{"b.txt"}, false); err != nil || !created {
		t.Fatal(created, err)
	}
	folder, entries, err := s.LibraryFolder(testContext, "alice", lib.ID, nil)
	if err != nil || !folder.Collection() || len(entries) != 2 {
		t.Fatal(folder, entries, err)
	}
	if err = s.DeleteFile(testContext, "alice", lib.ID, []string{"docs"}, FileConditions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteFile(testContext, "alice", lib.ID, nil, FileConditions{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("library deleted as a file: %v", err)
	}
	if _, entries, err = s.LibraryFolder(testContext, "alice", lib.ID, nil); err != nil || len(entries) != 1 || entries[0].Name != "b.txt" {
		t.Fatal(entries, err)
	}
}

func TestPublishedNamesResolveConsistently(t *testing.T) {
	for _, filename := range []string{"a.txt", "Ä.txt"} {
		t.Run(filename, func(t *testing.T) { testPublishedNamesResolveConsistently(t, filename) })
	}
}

func testPublishedNamesResolveConsistently(t *testing.T, filename string) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, true)
	grantReader(t, s, w)
	old, _, err := s.PutFile(testContext, "alice", lib.ID, []string{filename}, PutFile{Blob: pdfContent(filename)})
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, old.Resource)
	if _, err = s.MoveFile(testContext, "alice", lib.ID, []string{filename}, []string{"renamed.txt"}, false); err != nil {
		t.Fatal(err)
	}
	// The rename is a draft: readers keep the published name, editors see the head.
	if f, err := s.LibraryFile(testContext, "reader", lib.ID, []string{strings.ToUpper(filename)}); err != nil || f.ID != old.ID {
		t.Fatal("reader lost the published name", err)
	}
	if _, err = s.LibraryFile(testContext, "alice", lib.ID, []string{filename}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("editor resolved the published name: %v", err)
	}
	// The head name is free, so another file may take it; once published it wins.
	fresh, _, err := s.PutFile(testContext, "alice", lib.ID, []string{filename}, PutFile{Blob: pdfContent(filename)})
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, fresh.Resource)
	f, err := s.LibraryFile(testContext, "reader", lib.ID, []string{filename})
	if err != nil || f.ID != fresh.ID {
		t.Fatal("head-name owner did not win", err)
	}
	_, entries, err := s.LibraryFolder(testContext, "reader", lib.ID, nil)
	if err != nil || len(entries) != 1 || entries[0].ID != fresh.ID {
		t.Fatalf("listing disagrees with resolution: %v %v", entries, err)
	}
	if _, err = s.WriteTarget(testContext, "reader", lib.ID, []string{"new.txt"}, FileConditions{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader may write: %v", err)
	}
}
