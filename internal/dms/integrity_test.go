package dms

import (
	"errors"
	"reflect"
	"testing"

	"papergo/internal/dal/dberr"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/revisions"
)

func TestPublishedSnapshotsAndKeywordIndex(t *testing.T) {
	s, w, list := fixture(t)
	i, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: "Item", Keywords: []string{"finance"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DAL.Resources().Change(testContext, i.ID, i.Version, "alice", resources.Changes{ParentID: &w.ID}); !errors.Is(err, dberr.ErrInvalidContainment) {
		t.Fatalf("DAL allowed invalid containment: %v", err)
	}
	pub, err := s.Publish(testContext, "alice", i.ID, i.Version)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := s.DAL.Revisions().Publications(testContext, revisions.PublicationFilter{ID: pub.ID})
	if err != nil || len(frozen) != 1 {
		t.Fatal(frozen, err)
	}
	page, err := s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Keyword: "finance"})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("keyword index: %+v %v", page, err)
	}
	keywords := []string{"legal"}
	if _, err = s.Update(testContext, "alice", i.ID, i.Version+1, UpdateResource{Keywords: &keywords}); err != nil {
		t.Fatal(err)
	}
	retained, err := s.DAL.Revisions().Publications(testContext, revisions.PublicationFilter{ID: pub.ID})
	if err != nil || len(retained) != 1 || !reflect.DeepEqual(frozen[0].Snapshot, retained[0].Snapshot) {
		t.Fatalf("content edit changed the published snapshot: %+v %v", retained, err)
	}
	page, err = s.Browse(testContext, "alice", Browse{WorkspaceID: w.ID, Keyword: "finance"})
	if err != nil || len(page.Data) != 0 {
		t.Fatal("old keyword still indexed")
	}
}

// staleScopes lists resources whose DAL-maintained scope differs from the
// nearest exclusive ancestor-or-self found by walking the hierarchy.
func staleScopes(t *testing.T, s *Service) []string {
	t.Helper()
	rows, err := s.DAL.Resources().Resources(testContext, resources.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*resources.Resource{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	stale := []string{}
	for _, row := range rows {
		at := row
		for at.InheritPermissions && at.ParentID != nil && byID[*at.ParentID] != nil {
			at = byID[*at.ParentID]
		}
		if row.ScopeID == nil || *row.ScopeID != at.ID {
			stale = append(stale, row.ID)
		}
	}
	return stale
}

func TestScopeFollowsInheritanceChanges(t *testing.T) {
	s, w, _ := fixture(t)
	// Without publishing, readers see items as soon as they exist.
	l, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "L"})
	if err != nil {
		t.Fatal(err)
	}
	scope := func(id string) string {
		t.Helper()
		r, err := s.DAL.Resources().Resource(testContext, id)
		if err != nil {
			t.Fatal(err)
		}
		if r.ScopeID == nil {
			return ""
		}
		return *r.ScopeID
	}
	setACL := func(id string, in Permissions) {
		t.Helper()
		r, err := s.Get(testContext, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetPermissions(testContext, "alice", id, r.Version, in); err != nil {
			t.Fatal(err)
		}
	}
	check := func(step string) {
		t.Helper()
		if stale := staleScopes(t, s); len(stale) > 0 {
			t.Fatalf("%s: stale scope_id on %v", step, stale)
		}
	}
	f := create(t, s, l.ID, "folder", "F", nil)
	nested := create(t, s, f.ID, "folder", "Nested", nil)
	deep := create(t, s, nested.ID, "item", "Deep", nil)
	item := create(t, s, f.ID, "item", "Item", nil)
	check("create")
	if scope(deep.ID) != w.ID {
		t.Fatal("new descendant did not inherit the workspace scope")
	}
	alice := Permission{PrincipalType: "user", PrincipalID: "alice", Action: "manage", Effect: "allow"}
	bob := Permission{PrincipalType: "user", PrincipalID: "bob", Action: "read", Effect: "allow"}
	setACL(nested.ID, Permissions{Grants: []Permission{alice}})
	setACL(l.ID, Permissions{Grants: []Permission{alice, bob}})
	check("break")
	if scope(item.ID) != l.ID || scope(deep.ID) != nested.ID {
		t.Fatalf("break: item %s deep %s", scope(item.ID), scope(deep.ID))
	}
	if _, err := s.Get(testContext, "bob", item.ID); err != nil {
		t.Fatal("grant on new scope not applied", err)
	}
	if _, err := s.Get(testContext, "bob", deep.ID); err == nil {
		t.Fatal("nested exclusive scope leaked the outer grant")
	}
	later := create(t, s, f.ID, "item", "Later", nil)
	if scope(later.ID) != l.ID {
		t.Fatal("item created under a broken scope did not join it")
	}
	setACL(l.ID, Permissions{Inherit: true})
	check("reset")
	if scope(later.ID) != w.ID || scope(deep.ID) != nested.ID {
		t.Fatalf("reset: later %s deep %s", scope(later.ID), scope(deep.ID))
	}
	if _, err := s.Get(testContext, "bob", item.ID); err == nil {
		t.Fatal("grant survived inheritance reset")
	}
}
