package dms

import (
	"context"
	"errors"
	"papergo/internal/dal/surfaces"
	"testing"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/dal/workflows"
)

func TestRecycleBinRestoreTreeAndPurge(t *testing.T) {
	s, _, c := fixture(t)
	folder := create(t, s, c.ID, "folder", "Folder", nil)
	older := create(t, s, folder.ID, "item", "Older deletion", nil)
	if err := s.Delete(testContext, "alice", older.ID, older.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	item := create(t, s, folder.ID, "item", "Recover me", nil)
	peer := create(t, s, c.ID, "item", "Peer", nil)
	typ := referencesType(t, s, c.WorkspaceID)
	edge, err := s.Link(testContext, "alice", peer.ID, CreateRelationship{Type: typ.ID, TargetID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, "alice", item.ID, latest(t, s, item.ID).Version); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	page, err := s.RecycleBin(testContext, "alice", c.ID, RecycleBinQuery{Limit: 1})
	if err != nil || len(page.Data) != 1 || page.NextCursor == "" || page.Data[0].DeletedBy != "alice" {
		t.Fatalf("bin: %+v %v", page, err)
	}
	if _, err = s.Get(testContext, "alice", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted item readable: %v", err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", c.ID, folder.ID, true); err != nil {
		t.Fatal(err)
	}
	live, err := s.Get(testContext, "alice", item.ID)
	if err != nil || *live.ParentID != folder.ID {
		t.Fatalf("restored child: %+v %v", live, err)
	}
	links, err := s.Relationships(testContext, "alice", peer.ID, "both", "", "", 50)
	if err != nil || len(links.Data) != 1 || links.Data[0].ID != edge.ID {
		t.Fatalf("restored relationships: %+v %v", links, err)
	}
	if _, err = s.Get(testContext, "alice", older.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("older deletion resurrected", err)
	}
	count, err := countSurfaces(s, surfaces.Filter{ItemID: item.ID})
	if err != nil || count != 2 {
		t.Fatal("surfaces", count, err)
	}
	if err = s.Delete(testContext, "alice", folder.ID, folder.Version+2, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(testContext, "alice", c.ID, folder.ID, false); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("nonrecursive purge: %v", err)
	}
	if err = s.PurgeDeleted(testContext, "alice", c.ID, folder.ID, true); err != nil {
		t.Fatal(err)
	}
	if n, err := countRevisions(s, item.ID); err != nil || n != 0 {
		t.Fatal("revisions retained", n, err)
	}
	if _, err = s.RestoreDeleted(testContext, "alice", c.ID, item.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("purged item restored", err)
	}
}

func TestRecycleBinAccessAndParentFallback(t *testing.T) {
	s, w, c := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, c.ID, "folder", "Folder", nil)
	item := create(t, s, folder.ID, "item", "Child", nil)
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	page, err := s.RecycleBin(testContext, "bob", c.ID, RecycleBinQuery{})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("reader can see bin", page, err)
	}
	if _, err = s.RestoreDeleted(testContext, "bob", c.ID, item.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("reader restored", err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(testContext, "bob", c.ID, item.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatal("contributor purged", err)
	}
	live, err := s.RestoreDeleted(testContext, "bob", c.ID, item.ID, false)
	if err != nil || *live.ParentID != c.ID {
		t.Fatalf("root fallback: %+v %v", live, err)
	}
}

type failingDeleter struct {
	fail bool
	keys []string
}

func (d *failingDeleter) Delete(_ context.Context, key string) error {
	if d.fail {
		return errors.New("storage unavailable")
	}
	d.keys = append(d.keys, key)
	return nil
}
func TestPurgeCleanupRetries(t *testing.T) {
	s, _, c := fixture(t)
	lib := create(t, s, c.WorkspaceID, "library", "Files", nil)
	item := create(t, s, lib.ID, "item", "File", nil)
	itemRun(t, s, lib.WorkspaceID, item.ID)
	if err := s.DAL.Runs().AddItem(testContext, "run", item.ID, 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, BlobInput{ObjectKey: "object", Filename: "file", ContentType: "application/pdf", Size: 1, SHA256: emptySHA256})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.DAL.Resources().Resource(testContext, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(testContext, "alice", item.ID, item.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(testContext, "alice", lib.ID, item.ID, false); err != nil {
		t.Fatal(err)
	}
	if run, err := s.DAL.Runs().Find(testContext, "run"); err != nil || run.ItemID != item.ID {
		t.Fatal("run audit lost", run, err)
	}
	if members, err := s.DAL.Runs().Items(testContext, []string{"run"}, ""); err != nil || len(members) != 1 || members[0].ItemID != item.ID {
		t.Fatal("run membership lost", members, err)
	}
	d := &failingDeleter{fail: true}
	if err = s.CleanupPurgedBlobs(testContext, d); err == nil {
		t.Fatal("storage failure lost")
	}
	d.fail = false
	if err = s.CleanupPurgedBlobs(testContext, d); err != nil || len(d.keys) != 1 {
		t.Fatal("retry", d.keys, err)
	}
	if queued, err := s.DAL.Blobs().Deletions(testContext, 10); err != nil || len(queued) != 0 {
		t.Fatal("cleanup queue", queued, err)
	}
}

func TestRecycleRestoreConflictRollsBack(t *testing.T) {
	s, w, _ := fixture(t)
	lib := create(t, s, w.ID, "library", "Files", nil)
	folder := create(t, s, lib.ID, "folder", "Folder", nil)
	item := create(t, s, folder.ID, "item", "File", nil)
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	create(t, s, lib.ID, "folder", "Folder", nil)
	if _, err := s.RestoreDeleted(testContext, "alice", lib.ID, folder.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal("name conflict accepted", err)
	}
	if r, err := s.DAL.Resources().Resource(testContext, item.ID); err != nil || r.DeletedAt == nil {
		t.Fatal("partial restore", r, err)
	}
}

func TestRecycleBinListsTopLevelDeletionsNewestFirst(t *testing.T) {
	s, _, c := fixture(t)
	folder := create(t, s, c.ID, "folder", "Contracts", nil)
	sub := create(t, s, folder.ID, "folder", "2024", nil)
	for _, name := range []string{"One", "Two"} {
		create(t, s, sub.ID, "item", name, nil)
	}
	alone := create(t, s, folder.ID, "item", "Deleted alone", nil)
	if err := s.Delete(testContext, "alice", alone.ID, alone.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	first := create(t, s, c.ID, "item", "First", nil)
	if err := s.Delete(testContext, "alice", first.ID, first.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(testContext, "alice", folder.ID, folder.Version, DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	names, with, after := []string{}, []int{}, ""
	for {
		page, err := s.RecycleBin(testContext, "alice", c.ID, RecycleBinQuery{TopLevel: true, Limit: 2, After: after})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Data {
			names, with = append(names, entry.Name), append(with, entry.DeletedWith)
		}
		if after = page.NextCursor; after == "" {
			break
		}
	}
	// The folder took its subfolder and two items; the item deleted earlier stays its own entry.
	if len(names) != 3 || names[0] != "Contracts" || with[0] != 3 || names[1] != "First" || names[2] != "Deleted alone" {
		t.Fatal(names, with)
	}
	all, err := s.RecycleBin(testContext, "alice", c.ID, RecycleBinQuery{})
	if err != nil || len(all.Data) != 6 {
		t.Fatal("every entry", len(all.Data), err)
	}
	if _, err = s.RecycleBin(testContext, "alice", c.ID, RecycleBinQuery{After: "nonsense"}); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

// itemRun records run "run" of a new workflow on an item.
func itemRun(t *testing.T, s *Service, workspaceID, itemID string) {
	t.Helper()
	wf, err := s.DAL.Workflows().Create(testContext, workflows.CreateInput{WorkspaceID: workspaceID, Key: "audit", Name: "Audit", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DAL.Runs().Create(testContext, runstore.CreateInput{ID: "run", WorkflowID: wf.ID, WorkflowVersion: 1, WorkspaceID: workspaceID, ItemID: itemID, EventID: "event", EventType: EventItemCreated, Actor: "alice"}); err != nil {
		t.Fatal(err)
	}
}
