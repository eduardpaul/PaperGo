package dms

import (
	"context"
	"errors"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"strings"
	"testing"
	"time"
)

type retryHomeDeletion struct {
	storage.Store
	fail bool
}

func (s *retryHomeDeletion) Delete(ctx context.Context, key string) error {
	if s.fail {
		s.fail = false
		return errors.New("temporary storage failure")
	}
	return s.Store.Delete(ctx, key)
}

func TestAccountDeletionPurgesHomeAndRetainsSharedContent(t *testing.T) {
	s, w, _ := fixture(t)
	ctx := t.Context()
	home, err := s.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUser(ctx, "organization-admin", "alice"); !errors.Is(err, identity.ErrLastWorkspaceOwner) {
		t.Fatal("shared owner guard", err)
	}
	if _, err = s.DAL.Resources().Resource(ctx, home.WorkspaceID); err != nil {
		t.Fatal("guard removed Home", err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shared := create(t, s, w.ID, "library", "Shared", nil)
	object, err := store.Put(ctx, strings.NewReader("%PDF-shared bytes"), 100)
	if err != nil {
		t.Fatal(err)
	}
	blob := BlobInput{ObjectKey: object.Key, Filename: "shared.pdf", ContentType: "application/pdf", Size: object.Size, SHA256: object.SHA256}
	private, err := s.CreateDocument(ctx, "alice", home.InboxLibraryID, DocumentMetadata{Name: "Private", Values: map[string]any{"description": "Private text"}}, blob)
	if err != nil {
		t.Fatal(err)
	}
	public, err := s.CreateDocument(ctx, "alice", shared.ID, DocumentMetadata{Name: "Shared"}, blob)
	if err != nil {
		t.Fatal(err)
	}
	exclusive, err := store.Put(ctx, strings.NewReader("%PDF-private only"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateDocument(ctx, "alice", home.DocumentsLibraryID, DocumentMetadata{Name: "Exclusive"}, BlobInput{ObjectKey: exclusive.Key, Filename: "private.pdf", ContentType: "application/pdf", Size: exclusive.Size, SHA256: exclusive.SHA256}); err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, home.DocumentsLibraryID, "folder", "Parent", nil)
	child := create(t, s, folder.ID, "folder", "Child", nil)
	create(t, s, child.ID, "item", "Deep item", nil)
	if _, err = s.Link(ctx, "alice", private.Item.ID, CreateRelationship{TargetID: public.Item.ID}); err != nil {
		t.Fatal(err)
	}
	peer, err := s.DAL.Resources().Resource(ctx, public.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.Directory().SaveGroup(ctx, "organization-admin", "", 0, identity.GroupInput{Name: "Personal destination"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetGroupInbox(ctx, "alice", group.ID, GroupInboxInput{LibraryID: home.InboxLibraryID}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, seed := range []struct {
		table string
		row   daltest.Row
	}{
		{"operations", daltest.Row{"id": "home-op", "created_at": now, "updated_at": now, "container_id": home.InboxLibraryID, "kind": "test", "trace_id": "trace", "created_by": "alice"}},
		{"workflows", daltest.Row{"id": "home-flow", "created_at": now, "updated_at": now, "workspace_id": home.WorkspaceID, "key": "test", "name": "Test", "current_version": 1, "created_by": "alice", "updated_by": "alice"}},
		{"workflow_versions", daltest.Row{"id": "home-version", "created_at": now, "workflow_id": "home-flow", "number": 1, "definition": "{}", "created_by": "alice"}},
		{"workflow_runs", daltest.Row{"id": "home-run", "created_at": now, "workflow_id": "home-flow", "workflow_version": 1, "workspace_id": home.WorkspaceID, "item_id": home.InboxLibraryID, "event_id": "event", "event_type": "item.created", "depth": 0, "actor": "alice"}},
		{"runner_step_results", daltest.Row{"run_id": "home-run", "step_id": 1, "step": "private", "output": []byte("private output")}},
	} {
		daltest.Insert(t, s.DAL, seed.table, seed.row)
	}
	if err = s.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	for _, left := range []struct {
		table string
		match daltest.Row
	}{
		{"resources", daltest.Row{"workspace_id": home.WorkspaceID}},
		{"item_revisions", daltest.Row{"container_id": home.InboxLibraryID}},
		{"group_inboxes", daltest.Row{"library_id": home.InboxLibraryID}},
		{"workflow_runs", daltest.Row{"workspace_id": home.WorkspaceID}},
		{"schema_revisions", daltest.Row{"container_id": home.InboxLibraryID}},
		{"operations", nil}, {"workflows", nil}, {"workflow_versions", nil}, {"runner_step_results", nil},
	} {
		if n := daltest.Count(t, s.DAL, left.table, left.match); n != 0 {
			t.Fatal(left.table, n)
		}
	}
	if _, err = s.DAL.Resources().Resource(ctx, private.Item.ID); !dberr.IsNotFound(err) {
		t.Fatal("private item survived", err)
	}
	survivor, err := s.Get(ctx, "bob", public.Item.ID)
	if err != nil || survivor.Version != peer.Version+1 {
		t.Fatal("peer not touched", survivor, err)
	}
	if _, err = s.Home(ctx, "alice"); !errors.Is(err, ErrForbidden) {
		t.Fatal("deleted user provisioned Home", err)
	}
	retry := &retryHomeDeletion{Store: store, fail: true}
	if err = s.CleanupPurgedBlobs(ctx, retry); err == nil {
		t.Fatal("storage failure ignored")
	}
	if queued, err := s.DAL.Blobs().Deletions(ctx, 10); err != nil || len(queued) == 0 {
		t.Fatal("retry queue lost", queued, err)
	}
	if err = s.CleanupPurgedBlobs(ctx, retry); err != nil {
		t.Fatal(err)
	}
	reader, err := store.Open(ctx, object.Key)
	if err != nil {
		t.Fatal("shared object deleted", err)
	}
	reader.Close()
	if reader, err = store.Open(ctx, exclusive.Key); err == nil {
		reader.Close()
		t.Fatal("private object survived cleanup")
	}
	if n, err := s.DAL.Audit().Count(ctx, auditstore.Filter{}); err != nil || n == 0 {
		t.Fatal("audit lost", n, err)
	}
}

func TestAccountDeletionHomeCleanupRollsBack(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := t.Context()
	home, err := s.Home(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	daltest.Reject(t, s.DAL, "resources", daltest.OnDelete, daltest.Row{"personal_owner_id": "bob"})
	if err = s.DeleteUser(ctx, "organization-admin", "bob"); err == nil {
		t.Fatal("forced deletion succeeded")
	}
	user, err := s.Directory().User(ctx, "organization-admin", "bob")
	if err != nil || !user.Active || user.DeletedAt != nil {
		t.Fatal(user, err)
	}
	lib, err := s.DAL.Resources().Resource(ctx, home.InboxLibraryID)
	if err != nil || lib.DeletedAt != nil {
		t.Fatal("partial cleanup", lib, err)
	}
	if n, err := countResources(s, resources.Filter{WorkspaceID: home.WorkspaceID}); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}
