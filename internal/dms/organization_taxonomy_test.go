package dms

import (
	"encoding/json"
	"errors"
	auditstore "papergo/internal/dal/audit"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/identity"
	"papergo/internal/model"
	"strings"
	"testing"
)

func taxonomyOnlyRole(t *testing.T, s *Service, subject string, scopes ...string) string {
	t.Helper()
	d := s.Directory()
	if err := d.SetAssignment(testContext, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: subject}, true); err != nil {
		t.Fatal(err)
	}
	r, err := d.SaveRole(testContext, "alice", "", 0, identity.RoleInput{Name: "Taxonomy " + subject, Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(testContext, "alice", r.ID, identity.Principal{Type: directory.PrincipalUser, ID: subject}, false); err != nil {
		t.Fatal(err)
	}
	return r.ID
}

func TestTaxonomyScopesIndependentOfWorkspaceAccess(t *testing.T) {
	s := NewService(daltest.Database(t).SQL)
	ctx := testContext
	role := taxonomyOnlyRole(t, s, "bob", "taxonomy.read")
	taxonomyOnlyRole(t, s, "carol", "taxonomy.read", "taxonomy.manage")
	// Organization vocabulary is available even when no workspaces exist.
	groups, err := s.TermGroups(ctx, "bob", "", 10)
	if err != nil || len(groups.Data) != 1 || !groups.Data[0].IsSystem {
		t.Fatal("organization initialization", groups, err)
	}
	group, err := s.CreateTermGroup(ctx, "carol", TermGroupInput{Name: "Corporate"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := s.CreateTermSet(ctx, "carol", TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.CreateTermSet(ctx, "carol", TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.CreateTerm(ctx, "bob", open.ID, TermInput{Name: "Ideas"})
	if err != nil {
		t.Fatal("taxonomy-only contributor", err)
	}
	kw, created, err := s.AddKeyword(ctx, "bob", KeywordInput{Name: "Lunch"})
	if err != nil || !created {
		t.Fatal("taxonomy-only keyword contribution", kw, created, err)
	}
	if _, err = s.CreateTerm(ctx, "carol", closed.ID, TermInput{Name: "Spain"}); err != nil {
		t.Fatal(err)
	}
	denied := map[string]func() error{
		"closed contributions": func() error { _, e := s.CreateTerm(ctx, "bob", closed.ID, TermInput{Name: "Italy"}); return e },
		"curated flags": func() error {
			_, e := s.CreateTerm(ctx, "bob", open.ID, TermInput{Name: "Reserved", AvailableAsKeyword: true})
			return e
		},
		"term updates": func() error {
			_, e := s.UpdateTerm(ctx, "bob", added.ID, added.Version, TermInput{Name: "Changed"})
			return e
		},
		"group creation": func() error { _, e := s.CreateTermGroup(ctx, "bob", TermGroupInput{Name: "Denied"}); return e },
		"CSV imports": func() error {
			_, e := s.ImportTerms(ctx, "bob", group.ID, strings.NewReader("Term Set Name,Level 1 Term\nImported,X\n"))
			return e
		},
		"package imports": func() error { _, e := s.ImportTaxonomy(ctx, "bob", TaxonomyPackage{}); return e },
		"popularity":      func() error { _, e := s.PopularKeywords(ctx, "bob", 10); return e },
		"audit":           func() error { _, e := s.OrganizationAudit(ctx, "bob", AuditQuery{Limit: 10}); return e },
		"promotion": func() error {
			_, e := s.PromoteKeyword(ctx, "bob", kw.ID, kw.Version, PromoteKeywordInput{TermSetID: closed.ID})
			return e
		},
	}
	before, err := s.DAL.Audit().Count(ctx, auditstore.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for name, fn := range denied {
		t.Run(name, func(t *testing.T) {
			if e := fn(); !errors.Is(e, ErrForbidden) {
				t.Fatal(e)
			}
		})
	}
	after, err := s.DAL.Audit().Count(ctx, auditstore.Filter{})
	if err != nil || before != after {
		t.Fatal("denied mutation left an audit", before, after, err)
	}
	if _, err = s.ImportTerms(ctx, "carol", group.ID, strings.NewReader("Term Set Name,Level 1 Term\nImported,X\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PromoteKeyword(ctx, "carol", kw.ID, kw.Version, PromoteKeywordInput{TermSetID: closed.ID}); err != nil {
		t.Fatal(err)
	}
	audit, err := s.OrganizationAudit(ctx, "alice", AuditQuery{Limit: 100})
	if err != nil || len(audit.Data) == 0 {
		t.Fatal(audit, err)
	}
	for _, e := range audit.Data {
		if e.TraceID == "" {
			t.Fatal("audit event lacks a trace", e)
		}
	}
	w := create(t, s, "", "workspace", "Private", nil)
	if _, err = s.Get(ctx, "bob", w.ID); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatal("taxonomy role granted content access", err)
	}
	if _, err = s.Get(ctx, "carol", w.ID); !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatal("curation role granted content access", err)
	}
	if _, err = s.Term(ctx, "bob", added.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().SetAssignment(ctx, "alice", role, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TermsByID(ctx, "bob", []string{added.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked reader could resolve terms", err)
	}
	if _, _, err = s.AddKeyword(ctx, "bob", KeywordInput{Name: "Denied"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("revoked reader contributed", err)
	}
}

func TestSharedTaxonomyAcrossWorkspacesAndDeletion(t *testing.T) {
	s, first, list := fixture(t)
	ctx := testContext
	taxonomyOnlyRole(t, s, "carol", "taxonomy.read", "taxonomy.manage")
	second := create(t, s, "", "workspace", "Engineering", nil)
	other := create(t, s, second.ID, "list", "Projects", nil)
	group, err := s.CreateTermGroup(ctx, "carol", TermGroupInput{Name: "Organization"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "carol", TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	old := mustTerm(t, s, set.ID, TermInput{Name: "Finance"})
	current := mustTerm(t, s, set.ID, TermInput{Name: "Accounting"})
	for _, c := range []string{list.ID, other.ID} {
		for _, f := range []CreateField{
			{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}},
			{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}},
		} {
			if _, err = s.CreateField(ctx, "alice", c, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	a := create(t, s, list.ID, "item", "Invoice", map[string]any{"topic": old.ID, "keywords": []any{"Budget"}})
	b := create(t, s, other.ID, "item", "Project", map[string]any{"topic": "finance", "keywords": []any{"budget"}})
	keyword := a.Values["keywords"].([]any)[0].(string)
	if b.Values["topic"] != old.ID || b.Values["keywords"].([]any)[0] != keyword {
		t.Fatal("vocabulary duplicated by workspace", a.Values, b.Values)
	}
	popular, err := s.PopularKeywords(ctx, "carol", 10)
	if err != nil || len(popular) != 1 || popular[0].Count != 2 {
		t.Fatal("organization popularity", popular, err)
	}
	if _, err = s.MergeTerm(ctx, "carol", old.ID, old.Version, MergeTermInput{TargetTermID: current.ID}); err != nil {
		t.Fatal(err)
	}
	events, err := s.DAL.Events().List(ctx, eventstore.Filter{Types: []string{EventTermMerged}})
	if err != nil || len(events) != 2 {
		t.Fatal("workspace subscribers were omitted", events, err)
	}
	workspaces := map[string]bool{}
	for _, e := range events {
		workspaces[e.WorkspaceID] = true
	}
	if !workspaces[first.ID] || !workspaces[second.ID] {
		t.Fatal(workspaces)
	}
	raw, _ := json.Marshal(current.ID)
	for _, c := range []string{list.ID, other.ID} {
		page, err := s.Query(ctx, "alice", c, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "topic", Op: "eq", Value: raw}}})
		if err != nil || len(page.Data) != 1 {
			t.Fatal("merged IDs did not resolve in both workspaces", page, err)
		}
	}
	folder := smartOK(t, s, SmartFolderInput{Name: "Corporate topic", WorkspaceID: &second.ID, Definition: SmartFolderDefinition{Mode: "query", Terms: []string{current.ID}}})
	page, err := s.QuerySmartFolder(ctx, "alice", folder.ID, SmartFolderQueryRequest{})
	if err != nil || len(page.Data) != 1 || page.Data[0].Item.ID != b.ID {
		t.Fatal("workspace-scoped smart folder", page, err)
	}
	if err = s.Delete(ctx, "alice", first.ID, latest(t, s, first.ID).Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TermSet(ctx, "carol", set.ID); err != nil {
		t.Fatal("workspace deletion removed organization vocabulary", err)
	}
	if _, err = s.Term(ctx, "carol", current.ID); err != nil {
		t.Fatal(err)
	}
	popular, err = s.PopularKeywords(ctx, "carol", 10)
	if err != nil || len(popular) != 1 || popular[0].Count != 1 {
		t.Fatal("deleted content counted", popular, err)
	}
	name := "Updated project"
	updated, err := s.Update(ctx, "alice", b.ID, b.Version, UpdateResource{Name: &name})
	if err != nil || updated.Values["topic"] != current.ID {
		t.Fatal("shared normalization after deletion", updated, err)
	}
	if _, err = s.CreateTermSet(ctx, "carol", TermSetInput{GroupID: group.ID, Key: "topics", Name: "Duplicate"}); !errors.Is(err, ErrConflict) {
		t.Fatal("term-set keys are not organization unique", err)
	}
}

func TestItemKeywordContributionsRequireTaxonomyRead(t *testing.T) {
	s, w, list := fixture(t)
	ctx := testContext
	if _, err := s.CreateField(ctx, "alice", list.ID, CreateField{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}}); err != nil {
		t.Fatal(err)
	}
	taxonomyOnlyRole(t, s, "bob", "workspace.read", "item.read", "item.write")
	if err := s.SetWorkspaceMember(ctx, "alice", w.ID, WorkspaceMember{Subject: "bob", Role: "member"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "bob", list.ID, CreateResource{Kind: "item", Name: "Denied", Values: map[string]any{"keywords": []any{"Unauthorized keyword"}}}); !errors.Is(err, ErrForbidden) {
		t.Fatal("item write bypassed contribution scope", err)
	}
	terms, err := s.Keywords(ctx, "alice", "Unauthorized", 10)
	if err != nil || len(terms) != 0 {
		t.Fatal("failed write left vocabulary behind", terms, err)
	}
}
