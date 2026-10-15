package dms

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"papergo/ent"
	"papergo/ent/domainevent"
)

func changeEvents(t *testing.T, s *Service, typ string) []*ent.DomainEvent {
	t.Helper()
	rows, err := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(typ)).Order(ent.Asc(domainevent.FieldID)).All(testContext)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPermissionsChangedEvents(t *testing.T) {
	s, w, list := fixture(t)
	folder := create(t, s, list.ID, "folder", "Folder", nil)
	item := create(t, s, folder.ID, "item", "Item", nil)
	commits := 0
	s.OnCommit(func() { commits++ })
	for _, r := range []*ent.Resource{list, folder, item} {
		updated, err := s.SetPermissions(WithCause(testContext, 1, "run"), "alice", r.ID, r.Version, Permissions{CopyInherited: true})
		if err != nil {
			t.Fatal(err)
		}
		events := changeEvents(t, s, EventPermissionsChanged)
		e := events[len(events)-1]
		if e.WorkspaceID != w.ID || e.CollectionID == nil || *e.CollectionID != list.ID || e.ResourceID == nil || *e.ResourceID != r.ID || e.Actor != "alice" || e.Depth != 2 || e.CauseRunID == nil || *e.CauseRunID != "run" {
			t.Fatalf("event context: %+v", e)
		}
		before, after := e.Data["previous"].(map[string]any), e.Data["current"].(map[string]any)
		if before["inherit"] != true || after["inherit"] != false || after["scope_id"] != r.ID || e.Data["resource_kind"] != string(r.Kind) || len(after["grants"].([]any)) != 3 {
			t.Fatal(e.Data)
		}
		// Reordered grants and omitted allow effects are the same ACL.
		p, err := s.Permissions(testContext, "alice", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		p.Grants[0], p.Grants[2] = p.Grants[2], p.Grants[0]
		for i := range p.Grants {
			p.Grants[i].Effect = ""
		}
		updated, err = s.SetPermissions(testContext, "alice", r.ID, updated.Version, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(changeEvents(t, s, EventPermissionsChanged)) != len(events) {
			t.Fatal("unchanged ACL raised an event")
		}
		if _, err = s.SetPermissions(testContext, "bob", r.ID, updated.Version, Permissions{Inherit: true}); !errors.Is(err, ErrForbidden) {
			t.Fatal(err)
		}
		if _, err = s.SetPermissions(testContext, "alice", r.ID, updated.Version-1, Permissions{Inherit: true}); !errors.Is(err, ErrPreconditionFailed) {
			t.Fatal(err)
		}
		abort := errors.New("abort ACL change")
		if err = s.Write(testContext, func(tx *Service) error {
			if _, err := tx.SetPermissions(testContext, "alice", r.ID, updated.Version, Permissions{Inherit: true}); err != nil {
				return err
			}
			return abort
		}); !errors.Is(err, abort) {
			t.Fatal(err)
		}
		if len(changeEvents(t, s, EventPermissionsChanged)) != len(events) || latest(t, s, r.ID).InheritPermissions {
			t.Fatal("failed write persisted an ACL event or change")
		}
		if _, err = s.SetPermissions(testContext, "alice", r.ID, updated.Version, Permissions{Inherit: true}); err != nil {
			t.Fatal(err)
		}
	}
	if commits != 6 || len(changeEvents(t, s, EventPermissionsChanged)) != 6 {
		t.Fatal("event delivery count", commits)
	}
}

func TestWorkspaceMembershipRaisesPermissionsChanged(t *testing.T) {
	s, w, _ := fixture(t)
	for _, role := range []string{"member", "member", "visitor"} {
		if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RemoveWorkspaceMember(testContext, "alice", w.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveWorkspaceMember(testContext, "alice", w.ID, "alice"); !errors.Is(err, ErrLastOwner) {
		t.Fatal(err)
	}
	events := changeEvents(t, s, EventPermissionsChanged)
	if len(events) != 3 {
		t.Fatal("membership event count", len(events))
	}
	for i, want := range [][2]string{{"", "member"}, {"member", "visitor"}, {"visitor", ""}} {
		e := events[i]
		if e.CollectionID != nil || e.ResourceID == nil || *e.ResourceID != w.ID || e.Data["resource_kind"] != "workspace" || e.Data["member_subject"] != "bob" || e.Data["previous_role"] != want[0] || e.Data["role"] != want[1] {
			t.Fatal(e)
		}
	}
}

func TestPermissionGrantChangesRaiseEventsWithoutInheritanceChange(t *testing.T) {
	s, w, _ := fixture(t)
	library := create(t, s, w.ID, "library", "Library", nil)
	for _, action := range []string{"read", "write", ""} {
		grants := []Permission{{PrincipalType: "user", PrincipalID: "alice", Action: "manage"}}
		if action != "" {
			grants = append(grants, Permission{PrincipalType: "user", PrincipalID: "bob", Action: action})
		}
		var err error
		library, err = s.SetPermissions(testContext, "alice", library.ID, library.Version, Permissions{Grants: grants})
		if err != nil {
			t.Fatal(err)
		}
	}
	events := changeEvents(t, s, EventPermissionsChanged)
	if len(events) != 3 {
		t.Fatal("grant change event count", len(events))
	}
	for i, e := range events {
		current := e.Data["current"].(map[string]any)
		grants := current["grants"].([]any)
		if e.CollectionID == nil || *e.CollectionID != library.ID || e.Data["resource_kind"] != "library" || current["inherit"] != false {
			t.Fatal(e)
		}
		if i < 2 {
			if len(grants) != 2 || grants[1].(map[string]any)["action"] != []string{"read", "write"}[i] {
				t.Fatal(current)
			}
		} else if len(grants) != 1 {
			t.Fatal("revoked grant remained in snapshot", current)
		}
		if i > 0 && e.Data["previous"].(map[string]any)["inherit"] != false {
			t.Fatal("grant-only change incorrectly changed inheritance", e.Data)
		}
	}
}

func TestTermChangedEventsFanOutAndRollback(t *testing.T) {
	s, first, _ := fixture(t)
	second := create(t, s, "", "workspace", "Other", nil)
	deleted := create(t, s, "", "workspace", "Deleted", nil)
	if err := s.Delete(testContext, "alice", deleted.ID, deleted.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateTermGroup(testContext, "alice", TermGroupInput{Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	term := mustTerm(t, s, set.ID, TermInput{Name: "Finance", Labels: map[string]string{"es": "Finanzas"}})
	in := TermInput{Name: "Accounting", Labels: map[string]string{"es": "Contabilidad"}, Description: "Curated"}
	updated, err := s.UpdateTerm(WithCause(testContext, 2, "curation"), "alice", term.ID, term.Version, in)
	if err != nil {
		t.Fatal(err)
	}
	events := changeEvents(t, s, EventTermChanged)
	if len(events) != 2 {
		t.Fatal("term event count", len(events))
	}
	workspaces := map[string]bool{first.ID: true, second.ID: true}
	for _, e := range events {
		if !workspaces[e.WorkspaceID] || e.ResourceID != nil || e.CollectionID != nil || e.Actor != "alice" || e.Depth != 3 || e.CauseRunID == nil || *e.CauseRunID != "curation" {
			t.Fatal(e)
		}
		delete(workspaces, e.WorkspaceID)
		before, after := e.Data["previous"].(map[string]any), e.Data["current"].(map[string]any)
		if e.Data["term_id"] != term.ID || e.Data["term_set_id"] != set.ID || e.Data["version"] != float64(updated.Version) || before["name"] != "Finance" || after["name"] != "Accounting" || after["labels"].(map[string]any)["es"] != "Contabilidad" || !reflect.DeepEqual(e.Data["changed_fields"], []any{"description", "labels", "name"}) {
			t.Fatal(e.Data)
		}
	}
	updated, err = s.UpdateTerm(testContext, "alice", term.ID, updated.Version, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTerm(testContext, "alice", term.ID, term.Version, TermInput{Name: "Stale"}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal(err)
	}
	if _, err = s.UpdateTerm(testContext, "bob", term.ID, updated.Version, TermInput{Name: "Denied"}); !errors.Is(err, ErrForbidden) {
		t.Fatal(err)
	}
	// An audit failure must roll back both the relabel and every workspace event.
	if _, err = s.db.Exec(`CREATE TRIGGER reject_term_audit BEFORE INSERT ON audit_events WHEN NEW.action='term.update' BEGIN SELECT RAISE(ABORT, 'reject audit'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateTerm(testContext, "alice", term.ID, updated.Version, TermInput{Name: "Rejected"}); err == nil {
		t.Fatal("audit failure was ignored")
	}
	stored, err := s.Term(testContext, "alice", term.ID)
	if err != nil || stored.Name != "Accounting" || stored.Version != updated.Version || len(changeEvents(t, s, EventTermChanged)) != 2 {
		t.Fatal("failed or unchanged update persisted an event", stored, err)
	}
}

func TestPermissionTargets(t *testing.T) {
	s, w, list := fixture(t)
	outer := create(t, s, list.ID, "folder", "Outer", nil)
	inner := create(t, s, outer.ID, "folder", "Inner", nil)
	deep := create(t, s, inner.ID, "item", "Deep", nil)
	near := create(t, s, outer.ID, "item", "Near", nil)
	loose := create(t, s, list.ID, "item", "Loose", nil)
	targets := func(e Event, limit int) []string {
		t.Helper()
		var ids []string
		for after := ""; ; {
			page, err := s.PermissionTargets(testContext, e, after, limit)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range page {
				ids = append(ids, p.ID)
			}
			if len(page) < limit {
				break
			}
			after = page[len(page)-1].ID
		}
		slices.Sort(ids)
		return ids
	}
	sorted := func(ids ...string) []string { slices.Sort(ids); return ids }
	setPermissions := func(r *ent.Resource, in Permissions) Event {
		t.Helper()
		current, err := s.Get(testContext, "alice", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetPermissions(testContext, "alice", r.ID, current.Version, in); err != nil {
			t.Fatal(err)
		}
		events := changeEvents(t, s, EventPermissionsChanged)
		row := events[len(events)-1]
		e := Event{ID: row.ID, Type: row.Type, WorkspaceID: row.WorkspaceID, ResourceID: *row.ResourceID, Data: row.Data}
		if row.CollectionID != nil {
			e.CollectionID = *row.CollectionID
		}
		return e
	}
	// The inner folder gets its own scope: it and what is inside it.
	if got := targets(setPermissions(inner, Permissions{CopyInherited: true}), 1); !slices.Equal(got, sorted(inner.ID, deep.ID)) {
		t.Fatalf("inner scope: %v", got)
	}
	// The list's own scope stops at the inner folder's.
	if got := targets(setPermissions(list, Permissions{CopyInherited: true}), 2); !slices.Equal(got, sorted(outer.ID, near.ID, loose.ID)) {
		t.Fatalf("list scope: %v", got)
	}
	// The inner folder inherits again: only its subtree, not the rest of the
	// list that shares the scope.
	if got := targets(setPermissions(inner, Permissions{Inherit: true}), 10); !slices.Equal(got, sorted(inner.ID, deep.ID)) {
		t.Fatalf("inner reset: %v", got)
	}
	// Membership reaches every entry of the workspace; deleted entries none.
	member := Event{Type: EventPermissionsChanged, WorkspaceID: w.ID, ResourceID: w.ID, Data: map[string]any{"action": "member.set"}}
	if got := targets(member, 3); !slices.Equal(got, sorted(outer.ID, inner.ID, deep.ID, near.ID, loose.ID)) {
		t.Fatalf("membership: %v", got)
	}
	loose = mustGet(t, s, loose.ID)
	if err := s.Delete(testContext, "alice", loose.ID, loose.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := targets(member, 3); slices.Contains(got, loose.ID) {
		t.Fatalf("deleted entry delivered: %v", got)
	}
}

func mustGet(t *testing.T, s *Service, id string) *ent.Resource {
	t.Helper()
	r, err := s.Get(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
