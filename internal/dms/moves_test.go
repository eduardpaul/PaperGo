package dms

import (
	"errors"
	"slices"
	"testing"

	"papergo/ent"
	"papergo/ent/grant"
	"papergo/ent/itemsurface"
)

func TestMoveInboxFileToWritableFolderInAnotherWorkspace(t *testing.T) {
	s, _, _ := fixture(t)
	ctx := t.Context()
	home, err := s.Home(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.CreateInboxDocument(ctx, "alice", "", home.InboxLibraryID, DocumentMetadata{
		Name: "Invoice.pdf", Keywords: []string{"Finance"}, Values: map[string]any{"description": "Incoming invoice"},
	}, content("invoice.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	team, err := s.Create(ctx, "bob", "", CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	dest, err := s.Create(ctx, "bob", team.ID, CreateResource{Name: "Team documents", TemplateKey: "documents"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.Create(ctx, "bob", dest.ID, CreateResource{Kind: "folder", Name: "Incoming"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "bob", team.ID, WorkspaceMember{Subject: "alice", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "bob", folder.ID, folder.Version, Permissions{Inherit: false, Grants: []Permission{
		{"user", "bob", "manage", "allow"}, {"user", "alice", "write", "allow"},
	}}); err != nil {
		t.Fatal(err)
	}
	item := doc.Item
	if _, err = s.MoveItem(ctx, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("read-only library root accepted a move", err)
	}
	moved, err := s.MoveItem(ctx, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID, ParentID: folder.ID})
	if err != nil {
		t.Fatal(err)
	}
	if moved.WorkspaceID != team.ID || *moved.ContainerID != dest.ID || *moved.ParentID != folder.ID ||
		!slices.Equal(moved.Keywords, item.Keywords) || moved.Values["description"] != "Incoming invoice" {
		t.Fatal("file location or metadata changed incorrectly", moved)
	}
	blob, err := s.GetBlob(ctx, "alice", moved.ID, "")
	if err != nil || blob.ObjectKey != doc.File.ObjectKey {
		t.Fatal("moved file must remain readable with its original bytes", blob, err)
	}
	page, err := s.Browse(ctx, "alice", Browse{ParentID: home.InboxLibraryID})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("moved file is still in Inbox", page, err)
	}
}

func moveDestination(t *testing.T, s *Service, workspace, kind, typeID string) *ent.Resource {
	t.Helper()
	c, err := s.Create(testContext, "alice", workspace, CreateResource{Kind: kind, Name: "Destination", ContentTypeIDs: []string{typeID}, PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMoveItemAcrossWorkspacesPreservesContentAndResetsACL(t *testing.T) {
	s, w, _ := fixture(t)
	source := create(t, s, w.ID, "library", "Source", nil)
	item := create(t, s, source.ID, "item", "File", nil)
	if _, err := s.AttachBlob(testContext, "alice", item.ID, item.Version, BlobInput{ObjectKey: "bytes", Filename: "file", ContentType: "application/pdf", Size: 5, SHA256: emptySHA256}); err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	if _, err := s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	item = latest(t, s, item.ID)
	peer := create(t, s, source.ID, "item", "Peer", nil)
	typ := referencesType(t, s, w.ID)
	edge, err := s.Link(testContext, "alice", item.ID, CreateRelationship{Type: typ.ID, TargetID: peer.ID})
	if err != nil {
		t.Fatal(err)
	}
	item, err = s.SetPermissions(testContext, "alice", item.ID, latest(t, s, item.ID).Version, Permissions{Inherit: false, Grants: []Permission{{"user", "alice", "manage", "allow"}, {"user", "bob", "write", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	other := create(t, s, "", "workspace", "Other", nil)
	wf, err := s.Client.Workflow.Create().SetWorkspaceID(w.ID).SetKey("audit").SetName("Audit").SetCurrentVersion(1).SetCreatedBy("alice").SetUpdatedBy("alice").Save(testContext)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Client.WorkflowRun.Create().SetID("run").SetWorkflowID(wf.ID).SetWorkflowVersion(1).SetWorkspaceID(w.ID).SetItemID(item.ID).SetEventID("event").SetEventType(EventItemCreated).SetDepth(0).SetActor("alice").Exec(testContext); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(testContext, "alice", other.ID, WorkspaceMember{Subject: "carol", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	dest := moveDestination(t, s, other.ID, "library", *item.ContentTypeID)
	head, published, versions := *item.HeadRevisionID, *item.PublishedRevisionID, revisionCount(t, s, item.ID)
	out, err := s.MoveItem(testContext, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID})
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != item.ID || out.WorkspaceID != other.ID || *out.ContainerID != dest.ID || *out.ParentID != dest.ID || !out.InheritPermissions || *out.HeadRevisionID != head || *out.PublishedRevisionID != published {
		t.Fatalf("move: %+v", out)
	}
	if revisionCount(t, s, item.ID) != versions {
		t.Fatal("move-only revision added")
	}
	if run, err := s.Client.WorkflowRun.Get(testContext, "run"); err != nil || run.WorkspaceID != other.ID {
		t.Fatal("run location not moved", run, err)
	}
	if blob, err := s.GetBlob(testContext, "carol", item.ID, ""); err != nil || blob.ObjectKey != "bytes" {
		t.Fatal("blob lost", blob, err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("source ACL carried", err)
	}
	if n, err := s.Client.Grant.Query().Where(grant.ResourceIDEQ(item.ID)).Count(testContext); err != nil || n != 0 {
		t.Fatal("source grants kept", n, err)
	}
	if links, err := s.Relationships(testContext, "alice", item.ID, "both", "", "", 50); err != nil || len(links.Data) != 1 || links.Data[0].ID != edge.ID {
		t.Fatal("links lost", links, err)
	}
	if links, err := s.Relationships(testContext, "carol", item.ID, "both", "", "", 50); err != nil || len(links.Data) != 0 {
		t.Fatal("source peer disclosed", links, err)
	}
	for _, collection := range []string{source.ID, dest.ID} {
		page, err := s.Query(testContext, "alice", collection, QueryRequest{Surface: "head"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range page.Data {
			if r.ID == item.ID {
				found = true
			}
		}
		if found != (collection == dest.ID) {
			t.Fatal("query location stale", collection, found)
		}
	}
	if _, err = s.Revision(testContext, "carol", item.ID, 1); err != nil {
		t.Fatal("history lost", err)
	}
}

func TestMoveItemDestinationAccessAndNameConflicts(t *testing.T) {
	s, w, _ := fixture(t)
	source := create(t, s, w.ID, "library", "Files", nil)
	item := create(t, s, source.ID, "item", "File", nil)
	dest := moveDestination(t, s, w.ID, "library", *item.ContentTypeID)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPermissions(testContext, "alice", dest.ID, dest.Version, Permissions{Inherit: false, Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveItem(testContext, "bob", item.ID, item.Version, MoveItem{CollectionID: dest.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("inaccessible destination accepted", err)
	}
	create(t, s, dest.ID, "item", "File", nil)
	if _, err := s.MoveItem(testContext, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID}); !errors.Is(err, ErrConflict) {
		t.Fatal("destination duplicate name accepted", err)
	}
	if raw := latest(t, s, item.ID); raw.Version != item.Version || *raw.ContainerID != source.ID {
		t.Fatal("failed move mutated source", raw)
	}
}

func TestMoveItemFailuresAreAtomic(t *testing.T) {
	s, w, c := fixture(t)
	item := create(t, s, c.ID, "item", "Item", nil)
	unsupported := create(t, s, w.ID, "list", "Unsupported", nil)
	if _, err := s.MoveItem(testContext, "alice", item.ID, item.Version, MoveItem{CollectionID: unsupported.ID}); err == nil {
		t.Fatal("unsupported type accepted")
	}
	dest := moveDestination(t, s, w.ID, "list", *item.ContentTypeID)
	if _, err := s.MoveItem(testContext, "alice", item.ID, item.Version+1, MoveItem{CollectionID: dest.ID}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale move", err)
	}
	if _, err := s.MoveItem(testContext, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID, ParentID: c.ID}); err == nil {
		t.Fatal("wrong destination parent accepted")
	}
	folder := create(t, s, c.ID, "folder", "Folder", nil)
	if _, err := s.MoveItem(testContext, "alice", folder.ID, folder.Version, MoveItem{CollectionID: dest.ID}); err == nil {
		t.Fatal("folder crossed collection")
	}
	if _, err := s.Client.ExecContext(testContext, `CREATE TRIGGER fail_move BEFORE INSERT ON item_surfaces WHEN new.container_id='`+dest.ID+`' BEGIN SELECT RAISE(ABORT,'injected move failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MoveItem(testContext, "alice", item.ID, item.Version, MoveItem{CollectionID: dest.ID}); err == nil {
		t.Fatal("injected failure ignored")
	}
	raw := latest(t, s, item.ID)
	if raw.Version != item.Version || *raw.ContainerID != c.ID {
		t.Fatal("partial move", raw)
	}
	if projection, err := s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(item.ID), itemsurface.SurfaceEQ(itemsurface.SurfaceHead)).Only(testContext); err != nil || projection.ContainerID != c.ID {
		t.Fatal("projection rollback", projection, err)
	}
}
