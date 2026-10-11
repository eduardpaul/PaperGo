package dms

import (
	"context"
	"errors"
	"papergo/ent"
	"papergo/ent/resource"
	"papergo/internal/identity"
	"papergo/internal/storage"
	"strings"
	"testing"
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
	if _, err = s.Client.Resource.Get(ctx, home.WorkspaceID); err != nil {
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
	peer, err := s.Client.Resource.Get(ctx, public.Item.ID)
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
	for _, statement := range []string{
		"INSERT INTO operations(id,created_at,updated_at,container_id,kind,trace_id,created_by) VALUES('home-op',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?,'test','trace','alice')",
		"INSERT INTO workflows(id,created_at,updated_at,workspace_id,key,name,current_version,created_by,updated_by) SELECT 'home-flow',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,workspace_id,'test','Test',1,'alice','alice' FROM resources WHERE id=?",
		"INSERT INTO workflow_versions(id,created_at,workflow_id,number,definition,created_by) SELECT 'home-version',CURRENT_TIMESTAMP,'home-flow',1,'{}','alice' FROM resources WHERE id=?",
		"INSERT INTO workflow_runs(id,created_at,workflow_id,workflow_version,workspace_id,item_id,event_id,event_type,depth,actor) SELECT 'home-run',CURRENT_TIMESTAMP,'home-flow',1,workspace_id,id,'event','item.created',0,'alice' FROM resources WHERE id=?",
		"INSERT INTO runner_step_results(run_id,step_id,step,output) SELECT 'home-run',1,'private','private output' FROM resources WHERE id=?",
	} {
		if _, err = s.db.ExecContext(ctx, statement, home.InboxLibraryID); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.DeleteUser(ctx, "organization-admin", "alice"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"SELECT count(*) FROM resources WHERE workspace_id=?",
		"SELECT count(*) FROM item_revisions WHERE container_id=?",
		"SELECT count(*) FROM group_inboxes WHERE library_id=?",
		"SELECT count(*) FROM workflow_runs WHERE workspace_id=?",
		"SELECT count(*) FROM schema_revisions WHERE container_id=?",
	} {
		id := home.WorkspaceID
		if strings.Contains(query, "container_id") || strings.Contains(query, "library_id") {
			id = home.InboxLibraryID
		}
		var n int
		if err = s.db.QueryRowContext(ctx, query, id).Scan(&n); err != nil || n != 0 {
			t.Fatal(query, n, err)
		}
	}
	for _, table := range []string{"operations", "workflows", "workflow_versions", "runner_step_results"} {
		var n int
		if err = s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	if _, err = s.Client.Resource.Get(ctx, private.Item.ID); !ent.IsNotFound(err) {
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
	if n, err := s.Client.BlobDeletion.Query().Count(ctx); err != nil || n == 0 {
		t.Fatal("retry queue lost", n, err)
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
	if n, err := s.Client.AuditEvent.Query().Count(ctx); err != nil || n == 0 {
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
	if _, err = s.db.ExecContext(ctx, "CREATE TRIGGER fail_purge BEFORE DELETE ON resources WHEN old.personal_owner_id='bob' BEGIN SELECT RAISE(ABORT,'forced purge failure');END;"); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteUser(ctx, "organization-admin", "bob"); err == nil {
		t.Fatal("forced deletion succeeded")
	}
	user, err := s.Directory().User(ctx, "organization-admin", "bob")
	if err != nil || !user.Active || user.DeletedAt != nil {
		t.Fatal(user, err)
	}
	lib, err := s.Client.Resource.Get(ctx, home.InboxLibraryID)
	if err != nil || lib.DeletedAt != nil {
		t.Fatal("partial cleanup", lib, err)
	}
	if n, err := s.Client.Resource.Query().Where(resource.WorkspaceIDEQ(home.WorkspaceID)).Count(ctx); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}
