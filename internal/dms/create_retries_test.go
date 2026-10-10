package dms

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"papergo/ent/auditevent"
	"papergo/ent/domainevent"
)

func TestCreateRetryReturnsCurrentItemWithoutMutation(t *testing.T) {
	s, _, c := fixture(t)
	id := uuid.NewString()
	i, created, err := s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: id, Kind: "item", Name: "Original"})
	if err != nil || !created || i.ID != id {
		t.Fatal("first create", i, created, err)
	}
	name := "Edited"
	i, err = s.Update(testContext, "alice", id, i.Version, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	before := revisionCount(t, s, id)
	for n := 0; n < 3; n++ {
		out, created, err := s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: id, Kind: "item", Name: "Ignored", Values: map[string]any{"unknown": "ignored"}})
		if err != nil || created || out.ID != id || out.Name != "Edited" || out.Version != i.Version {
			t.Fatal("retry mutated", out, created, err)
		}
	}
	if revisionCount(t, s, id) != before {
		t.Fatal("retry appended revision")
	}
	if n, err := s.Client.AuditEvent.Query().Where(auditevent.ResourceIDEQ(id), auditevent.ActionEQ("resource.create")).Count(testContext); err != nil || n != 1 {
		t.Fatal("duplicate audit", n, err)
	}
	if n, err := s.Client.DomainEvent.Query().Where(domainevent.ResourceIDEQ(id), domainevent.TypeEQ(EventItemCreated)).Count(testContext); err != nil || n != 1 {
		t.Fatal("duplicate event", n, err)
	}
}

func TestCreateRetryConcurrentAndFolderIDs(t *testing.T) {
	s, _, c := fixture(t)
	id := uuid.NewString()
	var wg sync.WaitGroup
	var inserted atomic.Int32
	for n := 0; n < 12; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, created, err := s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: id, Kind: "item", Name: "One item"})
			if err != nil || out.ID != id {
				t.Errorf("concurrent retry: %+v %v", out, err)
				return
			}
			if created {
				inserted.Add(1)
			}
		}()
	}
	wg.Wait()
	if inserted.Load() != 1 || revisionCount(t, s, id) != 1 {
		t.Fatal("duplicate creations", inserted.Load())
	}
	folderID := uuid.NewString()
	folder, created, err := s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: folderID, Kind: "folder", Name: "Folder"})
	if err != nil || !created {
		t.Fatal(folder, created, err)
	}
	again, created, err := s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: folderID, Kind: "folder", Name: "Ignored"})
	if err != nil || created || again.Version != folder.Version {
		t.Fatal(again, created, err)
	}
}

func TestCreateRetryConflictsAndReadableRecovery(t *testing.T) {
	s, w, c := fixture(t)
	id := uuid.NewString()
	i, err := s.Create(testContext, "alice", c.ID, CreateResource{ID: id, Kind: "item", Name: "Item"})
	if err != nil {
		t.Fatal(err)
	}
	other := create(t, s, w.ID, "list", "Other", nil)
	if _, _, err = s.CreateWithStatus(testContext, "alice", other.ID, CreateResource{ID: id, Kind: "item"}); !errors.Is(err, ErrConflict) {
		t.Fatal("cross-collection ID accepted", err)
	}
	if _, _, err = s.CreateWithStatus(testContext, "bob", c.ID, CreateResource{ID: id, Kind: "item"}); !errors.Is(err, ErrConflict) {
		t.Fatal("inaccessible ID disclosed", err)
	}
	if _, err = s.Publish(testContext, "alice", id, i.Version); err != nil {
		t.Fatal(err)
	}
	i = latest(t, s, id)
	if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if out, created, err := s.CreateWithStatus(testContext, "bob", c.ID, CreateResource{ID: id, Kind: "item"}); err != nil || created || out.ID != id {
		t.Fatal("readable retry requires create ACL", out, created, err)
	}
	if err = s.Delete(testContext, "alice", id, i.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateWithStatus(testContext, "alice", c.ID, CreateResource{ID: id, Kind: "item", Name: "Replacement"}); !errors.Is(err, ErrConflict) {
		t.Fatal("deleted ID reused", err)
	}
	for _, bad := range []string{"invalid", uuid.Nil.String()} {
		if _, err = s.Create(testContext, "alice", c.ID, CreateResource{ID: bad, Kind: "item", Name: "Invalid"}); !isValidation(err) {
			t.Fatal("invalid ID accepted", bad, err)
		}
	}
	if _, err = s.Create(testContext, "alice", w.ID, CreateResource{ID: uuid.NewString(), Kind: "list", Name: "Invalid"}); !isValidation(err) {
		t.Fatal("collection caller ID accepted", err)
	}
}
