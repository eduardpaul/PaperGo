package dms

import (
	"errors"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	viewstore "papergo/internal/dal/views"
	"papergo/internal/storage"
	"reflect"
	"testing"
)

func TestArchiveLifecycleAndDiscovery(t *testing.T) {
	s, w, c := fixture(t)
	folder := create(t, s, c.ID, "folder", "Nested", nil)
	item := create(t, s, folder.ID, "item", "Keep me", nil)
	before := latest(t, s, item.ID)
	revs, err := countRevisions(s, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetArchived(testContext, "alice", item.ID, before.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived || archived.ArchivedAt == nil || archived.ArchivedBy == nil || *archived.ArchivedBy != "alice" || archived.Version != before.Version+1 {
		t.Fatalf("archive: %+v", archived)
	}
	if !reflect.DeepEqual(before.HeadRevisionID, archived.HeadRevisionID) || !reflect.DeepEqual(before.PublishedRevisionID, archived.PublishedRevisionID) || !before.UpdatedAt.Equal(archived.UpdatedAt) || before.UpdatedBy != archived.UpdatedBy || *archived.ParentID != folder.ID {
		t.Fatal("archive changed content or location")
	}
	afterRevs, err := countRevisions(s, item.ID)
	if err != nil || revs != afterRevs {
		t.Fatal("archive created a revision", err)
	}
	repeated, err := s.SetArchived(testContext, "alice", item.ID, archived.Version, true)
	if err != nil || repeated.Version != archived.Version {
		t.Fatal("repeat changed version", err)
	}
	bulk, e := s.Bulk(testContext, "alice", c.ID, BulkRequest{Operations: []BulkOperation{{Action: "archive", ID: item.ID, Version: archived.Version}}})
	if e != nil || len(bulk.Data) != 1 || bulk.Data[0].Version != archived.Version {
		t.Fatal("no-op bulk returned a nonexistent version", bulk, e)
	}
	if _, err = s.SetArchived(testContext, "alice", item.ID, before.Version, false); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale archive accepted", err)
	}
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: "Keep"})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("archived item in browse", page, err)
	}
	for _, sel := range []refs.ArchiveSelection{refs.ArchiveActive, refs.ArchiveArchived, refs.ArchiveAll} {
		result, err := s.Query(testContext, "alice", c.ID, QueryRequest{IncludeTotal: true, Query: QuerySpec{Archive: sel}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range result.Data {
			found = found || r.ID == item.ID
		}
		if found != (sel != refs.ArchiveActive) {
			t.Fatalf("query %s: %+v", sel, result)
		}
	}
	archive, err := s.ArchiveItems(testContext, "alice", ArchiveQuery{Search: "keep"})
	if err != nil || len(archive.Data) != 1 || archive.Data[0].ID != item.ID || !archive.Data[0].CanWrite {
		t.Fatal("global archive", archive, err)
	}
	name := "Still editable"
	edited, err := s.Update(testContext, "alice", item.ID, archived.Version, UpdateResource{Name: &name})
	if err != nil || !edited.Archived {
		t.Fatal("edit lost archive", err)
	}
	if err = s.Delete(testContext, "alice", item.ID, edited.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	archive, err = s.ArchiveItems(testContext, "alice", ArchiveQuery{})
	if err != nil || len(archive.Data) != 0 {
		t.Fatal("deleted in archive", err)
	}
	restored, err := s.RestoreDeleted(testContext, "alice", c.ID, item.ID, false)
	if err != nil || !restored.Archived {
		t.Fatal("restore lost archive", err)
	}
	active, err := s.SetArchived(testContext, "alice", item.ID, restored.Version, false)
	if err != nil || active.Archived || active.ArchivedAt != nil || active.ArchivedBy != nil {
		t.Fatal("unarchive", err)
	}
	if _, err = s.SetArchived(testContext, "alice", folder.ID, folder.Version, true); err == nil {
		t.Fatal("folder archived")
	}
}

func TestArchivePermissionsPagingBulkAndView(t *testing.T) {
	s, w, c := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	first := create(t, s, c.ID, "item", "First", nil)
	second := create(t, s, c.ID, "item", "Second", nil)
	for _, item := range []*resources.Resource{first, second} {
		_, e := s.Publish(testContext, "alice", item.ID, item.Version)
		if e != nil {
			t.Fatal(e)
		}
		if _, err := s.SetArchived(testContext, "alice", item.ID, latest(t, s, item.ID).Version, true); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ArchiveItems(testContext, "bob", ArchiveQuery{Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == "" || page.Data[0].CanWrite {
		t.Fatal("reader archive", page, err)
	}
	next, err := s.ArchiveItems(testContext, "bob", ArchiveQuery{Limit: 1, After: page.NextCursor})
	if err != nil || len(next.Data) != 1 || next.Data[0].ID == page.Data[0].ID {
		t.Fatal("archive continuation", next, err)
	}
	if _, err = s.ArchiveItems(testContext, "alice", ArchiveQuery{After: page.NextCursor}); err == nil {
		t.Fatal("cursor not bound to caller")
	}
	if _, err = s.ArchiveItems(testContext, "bob", ArchiveQuery{After: page.NextCursor, Search: "Second"}); err == nil {
		t.Fatal("cursor not bound to filters")
	}
	a := latest(t, s, first.ID)
	b := latest(t, s, second.ID)
	if _, err = s.SetArchived(testContext, "bob", a.ID, a.Version, false); err == nil {
		t.Fatal("reader unarchived")
	}
	if _, err = s.Bulk(testContext, "alice", c.ID, BulkRequest{Operations: []BulkOperation{{Action: "unarchive", ID: a.ID, Version: a.Version}, {Action: "unarchive", ID: b.ID, Version: b.Version + 1}}}); err == nil {
		t.Fatal("stale bulk accepted")
	}
	if !latest(t, s, a.ID).Archived {
		t.Fatal("bulk partially committed")
	}
	views, err := s.Views(testContext, "alice", c.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var builtin *viewstore.ListView
	for i := range views.Data {
		if views.Data[i].SystemKey == viewstore.SystemArchived {
			builtin = &views.Data[i]
		}
	}
	if builtin == nil || builtin.IsDefault {
		t.Fatal("missing or default Archived view")
	}
	result, err := s.QueryView(testContext, "alice", builtin.ID, ViewQueryRequest{})
	if err != nil || len(result.Data) != 2 {
		t.Fatal("Archived view", result, err)
	}
	if err = s.DeleteView(testContext, "alice", builtin.ID, builtin.Version); err == nil {
		t.Fatal("deleted built-in")
	}
	if _, err = s.UpdateView(testContext, "alice", builtin.ID, builtin.Version, ViewInput{Name: "Changed", IsDefault: true}); err == nil {
		t.Fatal("changed built-in")
	}
}

func TestArchiveWebDAVKeepsDirectPathsAndNames(t *testing.T) {
	s, w, _ := fixture(t)
	library := davLibrary(t, s, w, false)
	file, _, err := s.PutFile(testContext, "alice", library.ID, []string{"Kept.pdf"}, PutFile{Blob: pdfContent("Kept.pdf")})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.SetArchived(testContext, "alice", file.ID, file.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	_, listed, err := s.LibraryFolder(testContext, "alice", library.ID, nil)
	if err != nil || len(listed) != 0 {
		t.Fatal("archived file listed", err)
	}
	direct, err := s.LibraryFile(testContext, "alice", library.ID, []string{"Kept.pdf"})
	if err != nil || direct.ID != file.ID {
		t.Fatal("direct path lost", err)
	}
	if _, err = s.Create(testContext, "alice", library.ID, CreateResource{Kind: "item", Name: "kept.PDF"}); !errors.Is(err, ErrConflict) {
		t.Fatal("archived name reused", err)
	}
	if _, err = s.SetArchived(testContext, "alice", file.ID, archived.Version, false); err != nil {
		t.Fatal(err)
	}
	_, listed, err = s.LibraryFolder(testContext, "alice", library.ID, nil)
	if err != nil || len(listed) != 1 {
		t.Fatal("unarchived file not listed", err)
	}
}

func TestArchiveDeltaAndPortableState(t *testing.T) {
	s, w, c := fixture(t)
	item := create(t, s, c.ID, "item", "Portable archive", nil)
	initial, err := s.Delta(testContext, "alice", c.ID, "", "", 50)
	if err != nil || initial.DeltaToken == "" {
		t.Fatal(initial, err)
	}
	archived, err := s.SetArchived(testContext, "alice", item.ID, item.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := s.Delta(testContext, "alice", c.ID, initial.DeltaToken, "", 50)
	if err != nil || len(changes.Data) != 1 || changes.Data[0].Removed != nil || !changes.Data[0].Archived {
		t.Fatal("archive disappeared from synchronization", changes, err)
	}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: w.ID})
	target := emptyPackageTarget(t)
	imported, err := importWorkspaceForTest(t, target, store, object, false, "")
	if err != nil {
		t.Fatal(err)
	}
	page, err := target.ArchiveItems(testContext, "organization-admin", ArchiveQuery{WorkspaceID: imported.WorkspaceID})
	if err != nil || len(page.Data) != 1 || !page.Data[0].Archived || page.Data[0].ArchivedBy == nil || *page.Data[0].ArchivedBy != "alice" || !page.Data[0].ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatal("portable archive state", page, err)
	}
	views, err := target.Views(testContext, "organization-admin", *page.Data[0].ContainerID, "", 50)
	if err != nil || len(views.Data) != 1 || views.Data[0].SystemKey != viewstore.SystemArchived {
		t.Fatal("portable built-in view", views, err)
	}
}

func TestArchiveWebDAVPublishedAliasCannotExposeDifferentPathTarget(t *testing.T) {
	s, w, _ := fixture(t)
	library := davLibrary(t, s, w, true)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	original, _, err := s.PutFile(testContext, "alice", library.ID, []string{"Report.pdf"}, PutFile{Blob: pdfContent("Report.pdf")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", original.ID, original.Version); err != nil {
		t.Fatal(err)
	}
	name := "Current.pdf"
	if _, err = s.Update(testContext, "alice", original.ID, latest(t, s, original.ID).Version, UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	owner, _, err := s.PutFile(testContext, "alice", library.ID, []string{"Report.pdf"}, PutFile{Blob: pdfContent("Report.pdf")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", owner.ID, owner.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetArchived(testContext, "alice", owner.ID, latest(t, s, owner.ID).Version, true); err != nil {
		t.Fatal(err)
	}
	_, listed, err := s.LibraryFolder(testContext, "bob", library.ID, nil)
	if err != nil || len(listed) != 0 {
		t.Fatal("published alias listed a different file than its direct path", listed, err)
	}
	direct, err := s.LibraryFile(testContext, "bob", library.ID, []string{"Report.pdf"})
	if err != nil || direct.ID != owner.ID {
		t.Fatal("archived direct path lost", direct, err)
	}
}
