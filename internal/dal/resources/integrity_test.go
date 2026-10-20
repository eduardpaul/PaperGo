package resources_test

import (
	"context"
	"errors"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"testing"
)

func TestResourcesHierarchyScopeCascadeAndPurge(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		// 1. Create user and workspace
		user, err := s.Directory().CreateUser(ctx, "user-alice", "Alice", "alice@example.com", true, false)
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}

		ws, err := s.Resources().Create(ctx, resources.New{
			ID:              "ws-1",
			WorkspaceID:     "ws-1",
			Kind:            refs.KindWorkspace,
			Name:            "Engineering",
			DefaultCurrency: "EUR",
			CreatedBy:       user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource workspace: %v", err)
		}
		if daltest.Val(ws.ScopeID) != ws.ID || ws.InheritPermissions {
			t.Fatalf("expected workspace scope_id=%s and inherit_permissions=false, got scope_id=%s inherit=%v",
				ws.ID, daltest.Val(ws.ScopeID), ws.InheritPermissions)
		}

		// 2. Create two library collections under workspace
		lib1, err := s.Resources().Create(ctx, resources.New{
			ID:          "lib-1",
			WorkspaceID: ws.ID,
			ParentID:    ws.ID,
			Kind:        refs.KindLibrary,
			Name:        "Contracts",
			CreatedBy:   user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource lib1: %v", err)
		}
		if daltest.Val(lib1.ScopeID) != ws.ID {
			t.Fatalf("expected lib1 to inherit workspace scope_id=%s, got %s", ws.ID, daltest.Val(lib1.ScopeID))
		}

		lib2, err := s.Resources().Create(ctx, resources.New{
			ID:          "lib-2",
			WorkspaceID: ws.ID,
			ParentID:    ws.ID,
			Kind:        refs.KindLibrary,
			Name:        "Archive",
			CreatedBy:   user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource lib2: %v", err)
		}

		// 3. Create field definition, content type, and attach to both libraries
		fd, err := s.Catalog().CreateField(ctx, catalog.FieldDefinition{ID: "fd-status", Key: "status", Label: "Status", Type: catalog.FieldChoice, Required: true, Indexed: true, Choices: []string{"draft", "signed"}})
		if err != nil {
			t.Fatalf("CreateFieldDefinition: %v", err)
		}
		ct, err := s.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "ct-doc", Key: "document", Name: "Document", FieldIds: []string{fd.ID}})
		if err != nil {
			t.Fatalf("CreateContentType: %v", err)
		}
		if _, err := s.Catalog().Attach(ctx, lib1.ID, ct.ID, true); err != nil {
			t.Fatalf("AttachCollectionContentType lib1: %v", err)
		}
		if _, err := s.Catalog().Attach(ctx, lib2.ID, ct.ID, true); err != nil {
			t.Fatalf("AttachCollectionContentType lib2: %v", err)
		}

		// 4. Create folder tree and item with free-text keywords (verifies keyword FK fix)
		folderA, err := s.Resources().Create(ctx, resources.New{
			ID:            "folder-a",
			WorkspaceID:   ws.ID,
			ParentID:      lib1.ID,
			ContainerID:   lib1.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindFolder,
			Name:          "2026",
			NameKey:       "2026",
			CreatedBy:     user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource folderA: %v", err)
		}
		folderB, err := s.Resources().Create(ctx, resources.New{
			ID:            "folder-b",
			WorkspaceID:   ws.ID,
			ParentID:      folderA.ID,
			ContainerID:   lib1.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindFolder,
			Name:          "Q4",
			NameKey:       "q4",
			CreatedBy:     user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource folderB: %v", err)
		}
		item1, err := s.Resources().Create(ctx, resources.New{
			ID:            "item-1",
			WorkspaceID:   ws.ID,
			ParentID:      folderB.ID,
			ContainerID:   lib1.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindItem,
			Name:          "MSA.pdf",
			NameKey:       "msa.pdf",
			Keywords:      []string{"legal", "urgent"}, // free-text strings automatically resolved to terms!
			CreatedBy:     user.ID,
		})
		if err != nil {
			t.Fatalf("CreateResource item1 with free-text keywords: %v", err)
		}
		if len(item1.Keywords) != 2 {
			t.Fatalf("expected 2 resolved keyword term IDs, got %v", item1.Keywords)
		}

		// 5. Break inheritance on folderA and verify scope_id cascades to folderB and item1
		if err := s.Resources().SetInheritPermissions(ctx, folderA.ID, folderA.Version, false); err != nil {
			t.Fatalf("SetResourceInheritPermissions: %v", err)
		}
		reloadedItem, err := s.Resources().Resource(ctx, item1.ID)
		if err != nil {
			t.Fatalf("FindResource item1: %v", err)
		}
		if daltest.Val(reloadedItem.ScopeID) != folderA.ID {
			t.Fatalf("expected item1 scope_id to cascade to folderA (%s), got %s", folderA.ID, daltest.Val(reloadedItem.ScopeID))
		}

		// 6. Hierarchy cycle detection: moving folderA under folderB must fail with ErrCycleDetected
		err = daltest.Move(ctx, s, folderA.ID, folderB.ID)
		if !errors.Is(err, dberr.ErrCycleDetected) {
			t.Fatalf("expected ErrCycleDetected when moving folderA below folderB, got %v", err)
		}

		// 7. Transfer item1 to lib2, which has the workspace scope
		if _, err := s.Resources().Transfer(ctx, item1.ID, lib2.ID, lib2.ID, "msa.pdf", nil, user.ID); err != nil {
			t.Fatalf("Transfer item1 to lib2: %v", err)
		}
		movedItem, err := s.Resources().Resource(ctx, item1.ID)
		if err != nil {
			t.Fatalf("FindResource movedItem: %v", err)
		}
		if daltest.Val(movedItem.ContainerID) != lib2.ID || daltest.Val(movedItem.ScopeID) != ws.ID {
			t.Fatalf("expected movedItem container=%s scope=%s, got container=%s scope=%s",
				lib2.ID, ws.ID, daltest.Val(movedItem.ContainerID), daltest.Val(movedItem.ScopeID))
		}

		// 8. Soft-delete, reject tombstone edits, restore, then purge workspace (verifying cascade delete fix)
		if err := daltest.Remove(ctx, s, item1.ID, user.ID); err != nil {
			t.Fatalf("SoftDeleteResource: %v", err)
		}
		err = daltest.Rename(ctx, s, item1.ID, "Edited", user.ID)
		if !errors.Is(err, dberr.ErrImmutable) {
			t.Fatalf("expected ErrImmutable when editing soft-deleted resource, got %v", err)
		}
		if err := daltest.Restore(ctx, s, item1.ID, "msa-restored.pdf", user.ID); err != nil {
			t.Fatalf("RestoreResource: %v", err)
		}

		// Purge workspace and verify cascade deletes all children cleanly
		if err := s.Resources().Purge(ctx, ws.ID); err != nil {
			t.Fatalf("PurgeResource workspace: %v", err)
		}
		exists, err := daltest.Exists(ctx, s, item1.ID)
		if err != nil || exists {
			t.Fatalf("expected item1 to be cascaded on workspace purge, exists=%v err=%v", exists, err)
		}
	})
}

func TestSubtreeLifecycle(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		f := daltest.NewFixture(t, newStore(t))
		if err := daltest.Remove(ctx, f.Store, f.Folder, "alice"); err != nil {
			t.Fatal(err)
		}
		item, err := f.Store.Resources().Resource(ctx, f.Item)
		if err != nil || item.DeletedAt == nil {
			t.Fatalf("descendant stayed live: %+v, %v", item, err)
		}
		daltest.AssertCounts(t, f, f.Source, 0)
		if err := daltest.Restore(ctx, f.Store, f.Item, "item", "alice"); !errors.Is(err, dberr.ErrInvalidContainment) {
			t.Fatalf("restore into deleted parent: %v", err)
		}
		if err := daltest.Restore(ctx, f.Store, f.Folder, "folder", "alice"); err != nil {
			t.Fatal(err)
		}
		if err := daltest.Restore(ctx, f.Store, f.Item, "item", "alice"); err != nil {
			t.Fatal(err)
		}
		daltest.Project(t, f)
		daltest.AssertCounts(t, f, f.Source, 4)
		if err := f.Store.Resources().Purge(ctx, f.Folder); err != nil {
			t.Fatal(err)
		}
		exists, err := daltest.Exists(ctx, f.Store, f.Item)
		if err != nil || exists {
			t.Fatalf("purge orphaned descendant: exists=%v err=%v", exists, err)
		}
		daltest.AssertCounts(t, f, f.Source, 0)
	})
}

func TestFailedTransferRollsBack(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		f := daltest.NewFixture(t, newStore(t))
		if _, err := f.Store.Resources().Create(ctx, resources.New{ID: "other", WorkspaceID: f.Workspace, ParentID: f.Destination, ContainerID: f.Destination, ContentTypeID: "type", Kind: refs.KindItem, Name: "Item", NameKey: "item"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Store.Resources().Transfer(ctx, f.Item, f.Destination, f.Destination, "item", nil, "alice"); !errors.Is(err, dberr.ErrUniqueViolation) {
			t.Fatalf("duplicate name accepted: %v", err)
		}
		item, err := f.Store.Resources().Resource(ctx, f.Item)
		if err != nil || daltest.Val(item.ContainerID) != f.Source {
			t.Fatalf("failed transfer changed item: %+v, %v", item, err)
		}
		daltest.AssertCounts(t, f, f.Source, 4)
		daltest.AssertCounts(t, f, f.Destination, 0)
	})
}

func TestNullableMetadataCanBeCleared(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		f := daltest.NewFixture(t, newStore(t))
		if err := daltest.Rename(context.Background(), f.Store, f.Item, "Item", "alice"); err != nil {
			t.Fatal(err)
		}
		item, err := f.Store.Resources().Resource(context.Background(), f.Item)
		if err != nil || item.NameKey != nil {
			t.Fatalf("nullable metadata not cleared: %+v, %v", item, err)
		}
	})
}
