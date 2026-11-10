package dms

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"papergo/internal/dal/content"
	"papergo/internal/dal/directory"
	"papergo/internal/identity"
)

// scopedView returns the names a reader's query lists in a collection, and
// requires the page, the total and the row-by-row check to agree: decisions
// kept between queries must never differ from deciding each row afresh.
func scopedView(t *testing.T, s *Service, subject, collection string) []string {
	t.Helper()
	var views [2][]string
	for i, rowACL := range []bool{false, true} {
		q, err := s.Query(testContext, subject, collection, QueryRequest{IncludeTotal: true, rowACL: rowACL})
		if err != nil {
			t.Fatal(subject, err)
		}
		for _, r := range q.Data {
			views[i] = append(views[i], r.Name)
		}
		slices.Sort(views[i])
		if counted(q.Total) != len(views[i]) {
			t.Fatal("total differs from the page", subject, rowACL, views[i], counted(q.Total))
		}
	}
	if !slices.Equal(views[0], views[1]) {
		t.Fatalf("kept decisions %v differ from row checks %v for %s", views[0], views[1], subject)
	}
	return views[0]
}

func wantView(t *testing.T, s *Service, step, subject, collection string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := scopedView(t, s, subject, collection); !slices.Equal(got, want) {
		t.Fatalf("%s: %s sees %v, want %v", step, subject, got, want)
	}
}

func publishedItem(t *testing.T, s *Service, parent, name string) string {
	t.Helper()
	r := create(t, s, parent, "item", name, nil)
	if _, err := s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func setGrants(t *testing.T, s *Service, id string, grants ...Permission) {
	t.Helper()
	r, err := s.Get(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(testContext, "alice", id, r.Version, Permissions{Grants: grants}); err != nil {
		t.Fatal(err)
	}
}

func resetGrants(t *testing.T, s *Service, id string) {
	t.Helper()
	r, err := s.Get(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(testContext, "alice", id, r.Version, Permissions{Inherit: true}); err != nil {
		t.Fatal(err)
	}
}

// Scope decisions are kept between queries. Every change to what they read
// must show in the next query: grants, memberships, roles, inheritance, and
// rows that bring a scope into a collection.
func TestKeptScopeDecisionsFollowAccessChanges(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	// A reader's first look at an empty collection must not hide what is
	// created in it afterwards.
	wantView(t, s, "empty", "reader", c.ID)
	publishedItem(t, s, c.ID, "Public")
	create(t, s, c.ID, "item", "Draft", nil)
	wantView(t, s, "first rows", "reader", c.ID, "Public")
	wantView(t, s, "first rows", "alice", c.ID, "Public", "Draft")

	// Grants on a scope of its own, given and taken away.
	secret := publishedItem(t, s, c.ID, "Secret")
	wantView(t, s, "inherited", "reader", c.ID, "Public", "Secret")
	setGrants(t, s, secret, Permission{"user", "alice", "manage", "allow"})
	wantView(t, s, "broken inheritance", "reader", c.ID, "Public")
	setGrants(t, s, secret, Permission{"user", "alice", "manage", "allow"}, Permission{"user", "reader", "read", "allow"})
	wantView(t, s, "granted", "reader", c.ID, "Public", "Secret")
	setGrants(t, s, secret, Permission{"user", "alice", "manage", "allow"})
	wantView(t, s, "revoked", "reader", c.ID, "Public")

	// Group membership, direct and nested.
	d := s.Directory()
	outer, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Auditors"})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Junior auditors"})
	if err != nil {
		t.Fatal(err)
	}
	setGrants(t, s, secret, Permission{"user", "alice", "manage", "allow"}, Permission{"group", outer.ID, "read", "allow"})
	wantView(t, s, "group without the reader", "reader", c.ID, "Public")
	member := identity.Principal{Type: directory.PrincipalUser, ID: "reader"}
	nested := identity.Principal{Type: directory.PrincipalGroup, ID: inner.ID}
	if err = d.SetMember(ctx, "alice", inner.ID, member, false); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "member of an unrelated group", "reader", c.ID, "Public")
	if err = d.SetMember(ctx, "alice", outer.ID, nested, false); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "nested member", "reader", c.ID, "Public", "Secret")
	if err = d.SetMember(ctx, "alice", outer.ID, nested, true); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "group unnested", "reader", c.ID, "Public")
	if err = d.SetMember(ctx, "alice", outer.ID, member, false); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "direct member", "reader", c.ID, "Public", "Secret")
	if err = d.SetMember(ctx, "alice", outer.ID, member, true); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "membership removed", "reader", c.ID, "Public")

	// A workspace role: members read drafts wherever the workspace grants.
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "workspace member", "reader", c.ID, "Public", "Draft")
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "workspace visitor again", "reader", c.ID, "Public")

	// Inheritance restored.
	resetGrants(t, s, secret)
	wantView(t, s, "inheritance restored", "reader", c.ID, "Public", "Secret")

	// A folder with a scope of its own, and rows joining it.
	folder := create(t, s, c.ID, "folder", "Board", nil)
	setGrants(t, s, folder.ID, Permission{"user", "alice", "manage", "allow"})
	wantView(t, s, "private folder", "reader", c.ID, "Public", "Secret")
	publishedItem(t, s, folder.ID, "Minutes")
	wantView(t, s, "row in a private folder", "reader", c.ID, "Public", "Secret")
	setGrants(t, s, folder.ID, Permission{"user", "alice", "manage", "allow"}, Permission{"user", "reader", "read_draft", "allow"})
	wantView(t, s, "folder opened", "reader", c.ID, "Public", "Secret", "Board", "Minutes")
	create(t, s, folder.ID, "item", "Agenda", nil)
	wantView(t, s, "draft joins an open folder", "reader", c.ID, "Public", "Secret", "Board", "Minutes", "Agenda")

	// Deleting and restoring a row with its own scope.
	setGrants(t, s, secret, Permission{"user", "alice", "manage", "allow"}, Permission{"user", "reader", "read", "allow"})
	gone, err := s.Get(ctx, "alice", secret)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "alice", gone.ID, gone.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "deleted", "reader", c.ID, "Public", "Board", "Minutes", "Agenda")
	if _, err = s.RestoreDeleted(ctx, "alice", c.ID, secret, false); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "restored", "reader", c.ID, "Public", "Secret", "Board", "Minutes", "Agenda")
}

// A row that moves to another collection gives up its own scope for the
// destination's, which decides who reads it there.
func TestKeptScopeDecisionsFollowMovesBetweenCollections(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	target := create(t, s, w.ID, "list", "Archive", nil)
	shared := publishedItem(t, s, c.ID, "Shared")
	setGrants(t, s, shared, Permission{"user", "alice", "manage", "allow"}, Permission{"user", "reader", "read", "allow"})
	private := publishedItem(t, s, c.ID, "Private")
	setGrants(t, s, private, Permission{"user", "alice", "manage", "allow"})
	wantView(t, s, "before", "reader", c.ID, "Shared")
	wantView(t, s, "before", "reader", target.ID)
	for _, id := range []string{shared, private} {
		r, err := s.Get(ctx, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.MoveItem(ctx, "alice", id, r.Version, MoveItem{CollectionID: target.ID}); err != nil {
			t.Fatal(err)
		}
	}
	wantView(t, s, "moved away", "reader", c.ID)
	wantView(t, s, "moved in", "reader", target.ID, "Shared", "Private")
	setGrants(t, s, private, Permission{"user", "alice", "manage", "allow"})
	wantView(t, s, "private again", "reader", target.ID, "Shared")
	wantView(t, s, "private again", "alice", target.ID, "Shared", "Private")
}

// Decisions made inside a transaction that rolls back read grants that never
// existed; they must not answer later queries.
func TestKeptScopeDecisionsForgetRolledBackGrants(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	publishedItem(t, s, c.ID, "Public")
	create(t, s, c.ID, "item", "Draft", nil)
	wantView(t, s, "before", "reader", c.ID, "Public")
	failed := errors.New("rolled back")
	err := s.write(ctx, func(t2 *Service) error {
		if err := t2.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "member"}); err != nil {
			return err
		}
		q, err := t2.Query(ctx, "reader", c.ID, QueryRequest{})
		if err != nil {
			return err
		}
		if len(q.Data) != 2 {
			t.Fatal("the transaction does not see its own grant", len(q.Data))
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatal(err)
	}
	wantView(t, s, "after the rollback", "reader", c.ID, "Public")
}

// Queries between access changes reuse one decision, and a credential's are
// never kept.
func TestScopeDecisionsAreKeptUntilAccessChanges(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	publishedItem(t, s, c.ID, "Public")
	reader := principal(ctx, "reader")
	first, err := s.DAL.Content().Scopes(ctx, c.ID, reader)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	// Content written into scopes the collection already has changes nothing.
	publishedItem(t, s, c.ID, "Another")
	again, err := s.DAL.Content().Scopes(ctx, c.ID, reader)
	if err != nil || again != first {
		t.Fatal("the decision was made again without an access change", err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.DAL.Content().Scopes(ctx, c.ID, reader)
	if err != nil || changed == first {
		t.Fatal("the decision outlived an access change", err)
	}
	token := identity.WithAccessToken(ctx, identity.AccessTokenIdentity{Subject: "reader", ID: "token", Scopes: []string{"item.read"}})
	one, err := s.DAL.Content().Scopes(token, c.ID, principal(token, "reader"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.DAL.Content().Scopes(token, c.ID, principal(token, "reader"))
	if err != nil || one == two {
		t.Fatal("a credential's decision was kept", err)
	}
}

// A collection with many scopes names them in one list, and one with more
// than MaxScopes checks each row; both list what deciding every row lists.
func TestManyScopesMatchRowChecks(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"Public"}
	publishedItem(t, s, c.ID, "Public")
	create(t, s, c.ID, "item", "Draft", nil)
	for i := 0; i < 70; i++ {
		name := fmt.Sprintf("Scoped %02d", i)
		id := publishedItem(t, s, c.ID, name)
		grants := []Permission{{"user", "alice", "manage", "allow"}}
		switch i % 3 {
		case 0:
			grants = append(grants, Permission{"user", "reader", "read", "allow"})
			want = append(want, name)
		case 1:
			grants = append(grants, Permission{"user", "reader", "read_draft", "allow"})
			want = append(want, name)
		}
		setGrants(t, s, id, grants...)
	}
	check := func(step string) {
		t.Helper()
		wantView(t, s, step, "reader", c.ID, want...)
		for _, surface := range []string{"head", "published"} {
			for _, subject := range []string{"alice", "reader"} {
				var views [2]int
				for i, rowACL := range []bool{false, true} {
					q, err := s.Query(ctx, subject, c.ID, QueryRequest{Surface: surface, IncludeTotal: true, Limit: 200, rowACL: rowACL})
					if err != nil {
						t.Fatal(step, surface, subject, err)
					}
					if views[i] = len(q.Data); counted(q.Total) != views[i] {
						t.Fatal(step, surface, subject, rowACL, views[i], counted(q.Total))
					}
				}
				if views[0] != views[1] {
					t.Fatal(step, surface, subject, views)
				}
			}
		}
	}
	check("listed scopes")
	limit := content.MaxScopes
	t.Cleanup(func() { content.MaxScopes = limit })
	content.MaxScopes = 8
	// Decisions kept under the larger limit are made again after a change.
	publishedItem(t, s, c.ID, "Late")
	setGrants(t, s, create(t, s, c.ID, "item", "Hidden", nil).ID, Permission{"user", "alice", "manage", "allow"})
	want = append(want, "Late")
	check("row checks")
}

// Decisions are kept through changes to nothing they read: rows moving or
// coming back within their scope, names, and another workspace's access.
// What they do read still makes them again: a user's status and a held
// role's scopes.
func TestScopeDecisionsOutliveWhatTheyDoNotRead(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	a := create(t, s, c.ID, "folder", "A", nil)
	b := create(t, s, c.ID, "folder", "B", nil)
	doc := publishedItem(t, s, a.ID, "Doc")
	create(t, s, c.ID, "item", "Draft", nil)
	reader := principal(ctx, "reader")
	kept := func() *content.Scopes {
		t.Helper()
		out, err := s.DAL.Content().Scopes(ctx, c.ID, reader)
		if err != nil || out == nil {
			t.Fatal(out, err)
		}
		return out
	}
	keeps := func(step string, change func()) {
		t.Helper()
		before := kept()
		change()
		if kept() != before {
			t.Fatal(step, "made the decision again")
		}
	}
	remakes := func(step string, change func()) {
		t.Helper()
		before := kept()
		change()
		if kept() == before {
			t.Fatal(step, "kept a decision made before it")
		}
	}
	d := s.Directory()
	keeps("a move within the scope", func() {
		r, err := s.Get(ctx, "alice", doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.MoveItem(ctx, "alice", doc, r.Version, MoveItem{CollectionID: c.ID, ParentID: b.ID}); err != nil {
			t.Fatal(err)
		}
	})
	keeps("a deletion and restore within the scope", func() {
		r, err := s.Get(ctx, "alice", doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Delete(ctx, "alice", doc, r.Version, DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.RestoreDeleted(ctx, "alice", c.ID, doc, false); err != nil {
			t.Fatal(err)
		}
	})
	keeps("a new name", func() {
		name := "Alice Liddell"
		if _, err := d.UpdateMe(ctx, "alice", &name); err != nil {
			t.Fatal(err)
		}
	})
	keeps("access in another workspace", func() {
		other := create(t, s, "", "workspace", "Elsewhere", nil)
		if err := s.SetWorkspaceMember(ctx, "alice", other.ID, WorkspaceMember{Subject: "reader", Role: "member"}); err != nil {
			t.Fatal(err)
		}
	})
	var role identity.Role
	keeps("a role nobody holds", func() {
		var err error
		if role, err = d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Auditor", Scopes: []string{"workspace.read", "item.read", "workspace.manage"}}); err != nil {
			t.Fatal(err)
		}
	})
	wantView(t, s, "before the role", "reader", c.ID, "A", "B", "Doc")
	remakes("the role assigned", func() {
		if err := d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: directory.PrincipalUser, ID: "reader"}, false); err != nil {
			t.Fatal(err)
		}
	})
	wantView(t, s, "with the role", "reader", c.ID, "A", "B", "Doc", "Draft")
	keeps("the role renamed", func() {
		var err error
		if role, err = d.SaveRole(ctx, "alice", role.ID, role.Version, identity.RoleInput{Name: "Senior auditor", Scopes: role.Scopes}); err != nil {
			t.Fatal(err)
		}
	})
	remakes("the role's scopes narrowed", func() {
		var err error
		if role, err = d.SaveRole(ctx, "alice", role.ID, role.Version, identity.RoleInput{Name: role.Name, Scopes: []string{"workspace.read", "item.read"}}); err != nil {
			t.Fatal(err)
		}
	})
	wantView(t, s, "role narrowed", "reader", c.ID, "A", "B", "Doc")
	remakes("the reader deactivated", func() {
		if _, err := d.SetUserActive(ctx, "alice", "reader", false); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := d.SetUserActive(ctx, "alice", "reader", true); err != nil {
		t.Fatal(err)
	}
	wantView(t, s, "reactivated", "reader", c.ID, "A", "B", "Doc")
}

// A row that takes a scope of its own with no grants brings its collection
// a scope its decisions have not seen; it must leave readers' pages and the
// group counts they read.
func TestKeptScopeDecisionsFollowAScopeWithoutGrants(t *testing.T) {
	s, w, c := fixture(t)
	ctx := testContext
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	fieldOK(t, s, c.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"Open", "Done"}, Indexed: true})
	lone := create(t, s, c.ID, "item", "Lone", map[string]any{"status": "Open"}).ID
	create(t, s, c.ID, "item", "Other", map[string]any{"status": "Open"})
	groups := func(step string) string {
		t.Helper()
		in := QueryRequest{Surface: "head", Query: QuerySpec{GroupBy: "status"}}
		kept, err := s.QueryGroups(ctx, "reader", c.ID, in)
		if err != nil {
			t.Fatal(step, err)
		}
		in.rowACL = true
		rows, err := s.QueryGroups(ctx, "reader", c.ID, in)
		if err != nil {
			t.Fatal(step, err)
		}
		if fmt.Sprint(kept.Data) != fmt.Sprint(rows.Data) {
			t.Fatal(step, "kept decisions count", kept.Data, "row checks", rows.Data)
		}
		return fmt.Sprint(rows.Data)
	}
	// Workspace members read drafts everywhere, so the counts kept for the
	// whole collection are theirs.
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	before := groups("inherited")
	r, err := s.Get(ctx, "alice", lone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", lone, r.Version, Permissions{}); err != nil {
		t.Fatal(err)
	}
	if after := groups("a scope without grants"); after == before {
		t.Fatal("the counts still hold the row", after)
	}
	wantView(t, s, "a scope without grants", "reader", c.ID, "Other")
}
