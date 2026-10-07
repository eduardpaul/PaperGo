package dms

import (
	"context"
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/itemrevision"
	"papergo/ent/itemsurface"
	"papergo/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
)

var testContext = context.Background()

func fixture(t *testing.T) (*Service, *ent.Resource, *ent.Resource) {
	t.Helper()
	s := NewService(testutil.Database(t).Client)
	w := create(t, s, "", "workspace", "Finance", nil)
	list := create(t, s, w.ID, "list", "Invoices", nil)
	return s, w, list
}
func create(t *testing.T, s *Service, parent, kind, name string, values map[string]any) *ent.Resource {
	t.Helper()
	r, err := s.Create(testContext, "alice", parent, CreateResource{Kind: kind, Name: name, Values: values, PublishingEnabled: kind == "list" || kind == "library"})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestFieldsSearchPublishingAndOwnership(t *testing.T) {
	s, w, list := fixture(t)
	_, err := s.CreateField(testContext, "alice", list.ID, CreateField{Key: "amount", Label: "Amount", Type: "number", Required: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Invalid", Values: map[string]any{"amount": "five"}}); err == nil {
		t.Fatal("incorrect field type accepted")
	}
	if _, err = s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Missing"}); err == nil {
		t.Fatal("required field missing")
	}
	folder := create(t, s, list.ID, "folder", "2026", nil)
	item := create(t, s, folder.ID, "item", "Quarterly invoice", map[string]any{"amount": 25.0})
	if item.ContainerID == nil || *item.ContainerID != list.ID {
		t.Fatal("folder item has wrong container")
	}
	if _, err = s.Create(testContext, "alice", item.ID, CreateResource{Kind: "library", Name: "Illegal"}); err == nil {
		t.Fatal("item was allowed to own a library")
	}
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: "Quarterly"})
	if err != nil || len(page.Data) != 1 || page.Data[0].ID != item.ID {
		t.Fatalf("search: %+v %v", page, err)
	}
	pub, err := s.Publish(testContext, "alice", item.ID, item.Version)
	if err != nil {
		t.Fatal(err)
	}
	name := "Updated invoice"
	updated, err := s.Update(testContext, "alice", item.ID, item.Version+1, UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err = json.Unmarshal(pub.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["name"] != "Quarterly invoice" || updated.Version != 3 {
		t.Fatal("snapshot changed or version not incremented")
	}
	page, err = s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Search: "Quarterly"})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("stale search index: %+v %v", page, err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish accepted: %v", err)
	}
	if _, err = s.Publications(testContext, "bob", item.ID, 0, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("publication leaked: %v", err)
	}
	if _, err = s.CreateField(testContext, "alice", list.ID, CreateField{Key: "new_required", Label: "Required", Type: "text", Required: true}); err == nil {
		t.Fatal("required field was added without a backfill")
	}
}
func TestAdditivePermissionsInheritanceAndRollback(t *testing.T) {
	s, w, list := fixture(t)
	item := create(t, s, list.ID, "item", "Secret", nil)
	_, err := s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"bob", "read_draft", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{Inherit: true, Grants: []Permission{{"bob", "read", "deny"}}})
	if err == nil {
		t.Fatal("deny grant accepted")
	}
	item, err = s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{Inherit: false, Grants: []Permission{{"alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inheritance break ignored: %v", err)
	}
	page, err := s.Browse(testContext, "bob", Browse{WorkspaceID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range page.Data {
		if r.ID == item.ID {
			t.Fatal("search leaked private resource")
		}
	}
	item, err = s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{Inherit: false, Grants: []Permission{{"alice", "manage", "allow"}, {"carol", "read_draft", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "carol", item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(testContext, "bob", item.ID); !errors.Is(err, ErrForbidden) {
		t.Fatal("inheritance not broken")
	}
	if _, err = s.SetPermissions(testContext, "alice", item.ID, item.Version, Permissions{Inherit: false}); err == nil {
		t.Fatal("lockout accepted")
	}
	still, err := s.Get(testContext, "alice", item.ID)
	if err != nil || still.Version != item.Version {
		t.Fatalf("failed permission update did not roll back: %v", err)
	}
	if _, err = s.Update(testContext, "carol", item.ID, item.Version, UpdateResource{Name: &item.Name}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reader could write: %v", err)
	}
}
func TestDirectionalRelationshipsAndHiddenTargets(t *testing.T) {
	s, w, list := fixture(t)
	source := create(t, s, list.ID, "item", "Source", nil)
	target := create(t, s, list.ID, "item", "Target", nil)
	link, err := s.Link(testContext, "alice", source.ID, CreateRelationship{TargetID: target.ID, Name: "references", InverseName: "referenced_by"})
	if err != nil {
		t.Fatal(err)
	}
	incoming, err := s.Relationships(testContext, "alice", target.ID, "incoming", "referenced_by", "", 10)
	if err != nil || len(incoming.Data) != 1 || incoming.Data[0].ID != link.ID {
		t.Fatalf("incoming relationship: %+v %v", incoming, err)
	}
	if _, err = s.Link(testContext, "alice", source.ID, CreateRelationship{TargetID: target.ID, Name: "references"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate relationship accepted: %v", err)
	}
	_, err = s.SetPermissions(testContext, "alice", w.ID, w.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"bob", "read_draft", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SetPermissions(testContext, "alice", target.ID, target.Version, Permissions{Inherit: false, Grants: []Permission{{"alice", "manage", "allow"}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Relationships(testContext, "bob", source.ID, "outgoing", "", "", 10)
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("relationship exposed hidden target: %+v %v", page, err)
	}
	w2 := create(t, s, "", "workspace", "Other", nil)
	l2 := create(t, s, w2.ID, "list", "Other list", nil)
	i2 := create(t, s, l2.ID, "item", "Other item", nil)
	if _, err = s.Link(testContext, "alice", source.ID, CreateRelationship{TargetID: i2.ID, Name: "cross_workspace"}); err == nil {
		t.Fatal("cross-workspace link accepted")
	}
}
func TestConcurrentUpdatesHaveOneWinner(t *testing.T) {
	s, _, list := fixture(t)
	item := create(t, s, list.ID, "item", "Original", nil)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := "Changed"
			_, err := s.Update(testContext, "alice", item.ID, 1, UpdateResource{Name: &name})
			if err == nil {
				winners.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("unexpected write failure: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("expected one winner, got %d", winners.Load())
	}
	current, err := s.Get(testContext, "alice", item.ID)
	if err != nil || current.Version != 2 {
		t.Fatalf("version: %+v %v", current, err)
	}
	count, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(item.ID)).Count(testContext)
	if err != nil || count != 2 {
		t.Fatalf("concurrent writers left revisions: %d %v", count, err)
	}
	count, err = s.Client.ItemSurface.Query().Where(itemsurface.ItemIDEQ(item.ID)).Count(testContext)
	if err != nil || count != 1 {
		t.Fatalf("concurrent writers left projections: %d %v", count, err)
	}
}
func TestLibraryRequiresBlobForPublishing(t *testing.T) {
	s, w, _ := fixture(t)
	library := create(t, s, w.ID, "library", "Contracts", nil)
	item := create(t, s, library.ID, "item", "Agreement", nil)
	if _, err := s.Publish(testContext, "alice", item.ID, 1); err == nil {
		t.Fatal("library item published without content")
	}
}
