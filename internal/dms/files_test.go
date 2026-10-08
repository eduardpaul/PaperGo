package dms

import (
	"errors"
	"github.com/google/uuid"
	"papergo/ent"
	"papergo/ent/auditevent"
	"papergo/ent/itemrevision"
	"papergo/ent/relationship"
	"papergo/ent/resource"
	"strings"
	"testing"
	"time"
)

func davLibrary(t *testing.T, s *Service, w *ent.Resource, publishing bool) *ent.Resource {
	t.Helper()
	lib, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "library", Name: "Shared", PublishingEnabled: publishing, WebDAVEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return lib
}
func content(name string) BlobInput {
	return BlobInput{ObjectKey: uuid.NewString(), Filename: name, ContentType: "text/plain", Size: 1, SHA256: strings.Repeat("a", 64)}
}
func revisionCount(t *testing.T, s *Service, id string) int {
	t.Helper()
	n, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(id)).Count(testContext)
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
	if err := s.Delete(testContext, "alice", report.ID, report.Version); err != nil {
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

func TestDeleteRetainsHistoryAndHidesTheSubtree(t *testing.T) {
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, false)
	folder := create(t, s, lib.ID, "folder", "Projects", nil)
	inner := create(t, s, folder.ID, "item", "Plan.txt", nil)
	outside := create(t, s, lib.ID, "item", "Index.txt", nil)
	typ := referencesType(t, s, w.ID)
	if _, err := s.Link(testContext, "alice", outside.ID, CreateRelationship{TypeID: typ.ID, TargetID: inner.ID}); err != nil {
		t.Fatal(err)
	}
	readers(t, s, current(t, s, w.ID))
	// An editor cannot remove content below that they could not change themselves.
	locked := current(t, s, inner.ID)
	if _, err := s.SetPermissions(testContext, "alice", inner.ID, locked.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"editor", "read", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "editor", folder.ID, folder.Version); !errors.Is(err, ErrForbidden) {
		t.Fatalf("editor deleted unwritable content: %v", err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version+1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete accepted: %v", err)
	}
	if err := s.Delete(testContext, "alice", lib.ID, current(t, s, lib.ID).Version); err == nil {
		t.Fatal("library deleted")
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{folder.ID, inner.ID} {
		if _, err := s.Get(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted resource readable: %v", err)
		}
		if _, err := s.Revisions(testContext, "alice", id, 0, 10); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted history readable: %v", err)
		}
	}
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
	if n, err := s.Client.Relationship.Query().Where(relationship.TargetIDEQ(inner.ID)).Count(testContext); err != nil || n != 0 {
		t.Fatalf("relationship to deleted item kept: %d %v", n, err)
	}
	event, err := s.Client.AuditEvent.Query().Where(auditevent.ResourceIDEQ(folder.ID), auditevent.ActionEQ("resource.delete")).Only(testContext)
	if err != nil || event.Details["descendants"] != float64(1) && event.Details["descendants"] != 1 {
		t.Fatalf("delete not audited: %+v %v", event, err)
	}
	// Tombstones are final.
	if _, err = s.Client.Resource.UpdateOneID(inner.ID).ClearDeletedAt().Save(testContext); err == nil {
		t.Fatal("tombstone restored")
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
	if _, err := s.SetPermissions(testContext, "alice", private.ID, private.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"bob", "read", "allow"}}}); err != nil {
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
	if _, err = s.Client.Resource.UpdateOneID(item.ID).SetParentID(list.ID).Save(testContext); err == nil {
		t.Fatal("trigger allowed a cross-collection move")
	}
	if _, err = s.Client.Resource.UpdateOneID(private.ID).SetParentID(nested.ID).Save(testContext); err == nil {
		t.Fatal("trigger allowed a cycle")
	}
	// Moving the item back out drops the folder's scope again.
	if _, err = s.Update(testContext, "alice", item.ID, moved.Version, UpdateResource{ParentID: &lib.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("scope kept after moving out: %v", err)
	}
	scope, err := s.Client.Resource.Query().Where(resource.IDEQ(item.ID)).Only(testContext)
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
	f, created, err := s.PutFile(testContext, "alice", lib.ID, []string{"docs", "a.txt"}, PutFile{Blob: content("a.txt")})
	if err != nil || !created || f.Blob == nil || f.Name != "a.txt" {
		t.Fatal(f, created, err)
	}
	// The first revision already carries the content.
	if n := revisionCount(t, s, f.ID); n != 1 {
		t.Fatalf("new file has %d revisions", n)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "A.TXT"}, PutFile{FileConditions: FileConditions{IfNoneMatch: "*"}, Blob: content("a.txt")}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("If-None-Match ignored: %v", err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, PutFile{FileConditions: FileConditions{IfMatch: `"stale"`}, Blob: content("a.txt")}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("If-Match ignored: %v", err)
	}
	f, created, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt"}, PutFile{FileConditions: FileConditions{IfMatch: f.ETag()}, Blob: content("a.txt")})
	if err != nil || created || revisionCount(t, s, f.ID) != 2 {
		t.Fatal(f, created, err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs"}, PutFile{Blob: content("Docs")}); !errors.Is(err, ErrExists) {
		t.Fatalf("file written over a folder: %v", err)
	}
	if _, _, err = s.PutFile(testContext, "alice", lib.ID, []string{"Docs", "a.txt", "x"}, PutFile{Blob: content("x")}); !errors.Is(err, ErrMissingParent) {
		t.Fatalf("file used as folder: %v", err)
	}
	other, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"b.txt"}, PutFile{Blob: content("b.txt")})
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
	s, w, _ := fixture(t)
	lib := davLibrary(t, s, w, true)
	grantReader(t, s, w)
	old, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"a.txt"}, PutFile{Blob: content("a.txt")})
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, old.Resource)
	if _, err = s.MoveFile(testContext, "alice", lib.ID, []string{"a.txt"}, []string{"renamed.txt"}, false); err != nil {
		t.Fatal(err)
	}
	// The rename is a draft: readers keep the published name, editors see the head.
	if f, err := s.LibraryFile(testContext, "reader", lib.ID, []string{"A.txt"}); err != nil || f.ID != old.ID {
		t.Fatal("reader lost the published name", err)
	}
	if _, err = s.LibraryFile(testContext, "alice", lib.ID, []string{"a.txt"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("editor resolved the published name: %v", err)
	}
	// The head name is free, so another file may take it; once published it wins.
	fresh, _, err := s.PutFile(testContext, "alice", lib.ID, []string{"a.txt"}, PutFile{Blob: content("a.txt")})
	if err != nil {
		t.Fatal(err)
	}
	publishItem(t, s, fresh.Resource)
	f, err := s.LibraryFile(testContext, "reader", lib.ID, []string{"a.txt"})
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

func TestWebDAVCredentials(t *testing.T) {
	s, _, _ := fixture(t)
	c, err := s.CreateWebDAVCredential(testContext, "alice", WebDAVCredentialInput{Label: "Laptop"})
	if err != nil || !strings.HasPrefix(c.Password, credentialPrefix) || c.SecretHash == c.Password {
		t.Fatal(c, err)
	}
	if subject, err := s.AuthenticateWebDAV(testContext, c.Password); err != nil || subject != "alice" {
		t.Fatal(subject, err)
	}
	if _, err = s.AuthenticateWebDAV(testContext, c.Password+"x"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong password accepted: %v", err)
	}
	// Labels are limited in characters, not bytes.
	if _, err = s.CreateWebDAVCredential(testContext, "alice", WebDAVCredentialInput{Label: strings.Repeat("文", 100)}); err != nil {
		t.Fatalf("100-character label refused: %v", err)
	}
	if _, err = s.CreateWebDAVCredential(testContext, "alice", WebDAVCredentialInput{Label: strings.Repeat("文", 101)}); err == nil {
		t.Fatal("101-character label accepted")
	}
	if _, err = s.CreateWebDAVCredential(testContext, "alice", WebDAVCredentialInput{Label: "Old", ExpiresAt: ptr(time.Now().Add(-time.Minute))}); err == nil {
		t.Fatal("expired credential issued")
	}
	soon, err := s.CreateWebDAVCredential(testContext, "alice", WebDAVCredentialInput{Label: "Soon", ExpiresAt: ptr(time.Now().Add(50 * time.Millisecond))})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err = s.AuthenticateWebDAV(testContext, soon.Password); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expired credential accepted: %v", err)
	}
	if list, err := s.WebDAVCredentials(testContext, "bob"); err != nil || len(list) != 0 {
		t.Fatal("credentials leaked to another principal", list, err)
	}
	if err = s.RevokeWebDAVCredential(testContext, "bob", c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked someone else's credential: %v", err)
	}
	if err = s.RevokeWebDAVCredential(testContext, "alice", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateWebDAV(testContext, c.Password); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked credential accepted: %v", err)
	}
}
