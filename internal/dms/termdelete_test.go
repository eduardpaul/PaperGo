package dms

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	eventstore "papergo/internal/dal/events"
	"papergo/internal/model"
	"papergo/internal/storage"
)

// inUse deletes a term that must be refused and returns what holds it.
func inUse(t *testing.T, s *Service, id string) TermUsage {
	t.Helper()
	term, err := s.Term(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	var held *TermInUse
	if err = s.DeleteTerm(testContext, "alice", id, term.Version); !errors.As(err, &held) || !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted a term in use: %v", err)
	}
	if _, err = s.Term(testContext, "alice", id); err != nil {
		t.Fatal("a refused delete removed the term", err)
	}
	return held.Usage
}

func deleteTerm(t *testing.T, s *Service, id string) {
	t.Helper()
	term, err := s.Term(testContext, "alice", id)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteTerm(testContext, "alice", id, term.Version); err != nil {
		t.Fatal("delete", term.Name, err)
	}
	if _, err = s.Term(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted term still read", err)
	}
}

func TestDeleteTerm(t *testing.T) {
	s, w, l := fixture(t)
	taxonomyOnlyRole(t, s, "reader", "taxonomy.read")
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []CreateField{
		{Key: "topic", Label: "Topic", Type: "term", Options: model.FieldOptions{TermSetID: set.ID}},
		{Key: "topics", Label: "Topics", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, Multiple: true}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(f.Key, err)
		}
	}

	// A keyword nothing holds goes, and its name is free again.
	junk, _, err := s.AddKeyword(testContext, "alice", KeywordInput{Name: "Junk"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteTerm(testContext, "alice", junk.ID, junk.Version+1); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatal("stale delete", err)
	}
	if err = s.DeleteTerm(testContext, "reader", junk.ID, junk.Version); !errors.Is(err, ErrForbidden) {
		t.Fatal("a reader deleted a keyword", err)
	}
	deleteTerm(t, s, junk.ID)
	again, created, err := s.AddKeyword(testContext, "alice", KeywordInput{Name: "junk"})
	if err != nil || !created || again.ID == junk.ID {
		t.Fatal("the name of a deleted keyword stayed taken", again, created, err)
	}
	events, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventTermDeleted}})
	if err != nil || len(events) != 1 || events[0].WorkspaceID != w.ID || events[0].Data["term_id"] != junk.ID || events[0].Data["name"] != "Junk" {
		t.Fatal("term.deleted", events, err)
	}
	// Its ID names nothing: a write that still gives it adds no keyword.
	if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Stale", Keywords: []string{junk.ID}}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatal("a deleted keyword's ID was accepted", err)
	}
	if found, err := s.Keywords(testContext, "alice", junk.ID, 5); err != nil || len(found) != 0 {
		t.Fatal("a deleted ID became a keyword", found, err)
	}

	// Items hold terms as keywords and in fields, indexed or not.
	keyword, _, _ := s.AddKeyword(testContext, "alice", KeywordInput{Name: "Budget"})
	plain := mustTerm(t, s, set.ID, TermInput{Name: "Plain"})
	indexed := mustTerm(t, s, set.ID, TermInput{Name: "Indexed"})
	item, err := s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{keyword.ID},
		Values: map[string]any{"topic": plain.ID, "topics": []any{indexed.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{keyword.ID, plain.ID, indexed.ID} {
		if usage := inUse(t, s, id); usage != (TermUsage{Items: 1}) {
			t.Fatal("item usage", usage)
		}
	}

	// The recycle bin holds them too: a restore brings the content back.
	if err = s.Delete(testContext, "alice", item.ID, item.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if usage := inUse(t, s, plain.ID); usage != (TermUsage{DeletedItems: 1}) {
		t.Fatal("recycle bin usage", usage)
	}
	item, err = s.RestoreDeleted(testContext, "alice", l.ID, item.ID, false)
	if err != nil || item.Values["topic"] != plain.ID {
		t.Fatal("restore", item, err)
	}

	// A revision that is no longer current does not hold a term. Restoring
	// it leaves the deleted terms out and adds none.
	item, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Keywords: &[]string{}, Values: &map[string]any{"topic": nil, "topics": []any{indexed.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	deleteTerm(t, s, keyword.ID)
	deleteTerm(t, s, plain.ID)
	if page, err := s.RevisionHistory(testContext, "alice", item.ID, 0, 10); err != nil || len(page.Data) != 2 {
		t.Fatal("history with deleted terms", page, err)
	}
	item, err = s.RestoreRevision(testContext, "alice", item.ID, 1, item.Version)
	if err != nil || len(item.Keywords) != 0 || item.Values["topic"] != nil || !slices.Equal(item.Values["topics"].([]any), []any{indexed.ID}) {
		t.Fatal("restored a revision with deleted terms", item, err)
	}
	if found, err := s.Keywords(testContext, "alice", keyword.ID, 5); err != nil || len(found) != 0 {
		t.Fatal("a restore added a keyword", found, err)
	}
	if usage := inUse(t, s, indexed.ID); usage != (TermUsage{Items: 1}) {
		t.Fatal("restored usage", usage)
	}

	// Everything else that names a term keeps it.
	parent := mustTerm(t, s, set.ID, TermInput{Name: "Parent"})
	mustTerm(t, s, set.ID, TermInput{Name: "Child", ParentID: &parent.ID})
	if usage := inUse(t, s, parent.ID); usage != (TermUsage{ChildTerms: 1}) {
		t.Fatal("child usage", usage)
	}
	filed := mustTerm(t, s, set.ID, TermInput{Name: "Filed"})
	raw, _ := json.Marshal(filed.ID)
	other := create(t, s, w.ID, "list", "Other", nil)
	defaulted, err := s.CreateField(testContext, "alice", other.ID, CreateField{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, DefaultValue: raw}})
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.CreateView(testContext, "alice", other.ID, ViewInput{Name: "Filed", Layout: "table", Columns: []string{"$name"}, Query: QuerySpec{Filter: &FilterExpr{Field: "topic", Op: "eq", Value: raw}}})
	if err != nil {
		t.Fatal(err)
	}
	folder := smartOK(t, s, SmartFolderInput{Name: "Filed", WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Mode: "query", Terms: []string{filed.ID}}})
	tagged, err := s.Update(testContext, "alice", other.ID, latest(t, s, other.ID).Version, UpdateResource{Keywords: &[]string{filed.ID}})
	if err == nil {
		t.Fatal("a term that is no keyword tagged a collection", tagged)
	}
	if usage := inUse(t, s, filed.ID); usage != (TermUsage{Fields: 1, Views: 1, SmartFolders: 1}) {
		t.Fatal("definition usage", usage)
	}
	if err = s.DeleteView(testContext, "alice", view.ID, view.Version); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteField(testContext, "alice", other.ID, defaulted.ID, latest(t, s, other.ID).Version); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteSmartFolder(testContext, "alice", folder.ID, folder.Version); err != nil {
		t.Fatal(err)
	}
	deleteTerm(t, s, filed.ID)
	label, _, _ := s.AddKeyword(testContext, "alice", KeywordInput{Name: "Shelf"})
	if _, err = s.Update(testContext, "alice", other.ID, latest(t, s, other.ID).Version, UpdateResource{Keywords: &[]string{label.ID}}); err != nil {
		t.Fatal(err)
	}
	if usage := inUse(t, s, label.ID); usage != (TermUsage{Collections: 1}) {
		t.Fatal("collection keyword usage", usage)
	}
}

func TestDeleteTermWithMergedTerms(t *testing.T) {
	s, _, l := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}}); err != nil {
		t.Fatal(err)
	}
	finance := mustTerm(t, s, set.ID, TermInput{Name: "Finance"})
	money := mustTerm(t, s, set.ID, TermInput{Name: "Money"})
	shelf := mustTerm(t, s, set.ID, TermInput{Name: "Shelf"})
	coins := mustTerm(t, s, set.ID, TermInput{Name: "Coins", ParentID: &shelf.ID})
	item := create(t, s, l.ID, "item", "Ledger", map[string]any{"topic": money.ID})
	if _, err = s.MergeTerm(testContext, "alice", money.ID, money.Version, MergeTermInput{TargetTermID: finance.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTerm(testContext, "alice", coins.ID, coins.Version, MergeTermInput{TargetTermID: finance.ID}); err != nil {
		t.Fatal(err)
	}
	merged, _ := s.Term(testContext, "alice", money.ID)
	if err = s.DeleteTerm(testContext, "alice", money.ID, merged.Version); err == nil || errors.Is(err, ErrConflict) {
		t.Fatal("merged terms are read-only", err)
	}
	// The item still holds the merged term's ID, which stands for Finance.
	if usage := inUse(t, s, finance.ID); usage != (TermUsage{Items: 1}) {
		t.Fatal("usage through a merged term", usage)
	}
	// A term merged elsewhere does not hold the term it once sat below.
	deleteTerm(t, s, shelf.ID)
	if pointer, err := s.Term(testContext, "alice", coins.ID); err != nil || pointer.ParentID != nil || pointer.Path != "/"+coins.ID+"/" || *pointer.MergedIntoID != finance.ID {
		t.Fatal("merged term below a deleted term", pointer, err)
	}
	if _, err = s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &map[string]any{"topic": nil}}); err != nil {
		t.Fatal(err)
	}
	deleteTerm(t, s, finance.ID)
	for _, id := range []string{money.ID, coins.ID} {
		if _, err = s.Term(testContext, "alice", id); !errors.Is(err, ErrNotFound) {
			t.Fatal("a term merged into a deleted term stayed", err)
		}
	}
	events, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventTermDeleted}})
	if err != nil || len(events) != 2 || events[1].Data["term_id"] != finance.ID || len(events[1].Data["merged_term_ids"].([]any)) != 2 {
		t.Fatal("term.deleted", events, err)
	}
	// Both names are free: nothing of the merge is left.
	mustTerm(t, s, set.ID, TermInput{Name: "Finance"})
	mustTerm(t, s, set.ID, TermInput{Name: "Money"})
	empty, err := s.TermSet(testContext, "alice", set.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteTermSet(testContext, "alice", set.ID, empty.Version); err == nil {
		t.Fatal("deleted a set with terms")
	}
}

// A workspace package carries history as it was written: revisions and
// publications that name terms deleted since travel with it.
func TestWorkspacePackageWithDeletedTerms(t *testing.T) {
	s, w, l := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}}); err != nil {
		t.Fatal(err)
	}
	keyword, _, err := s.AddKeyword(testContext, "alice", KeywordInput{Name: "Budget"})
	if err != nil {
		t.Fatal(err)
	}
	topic := mustTerm(t, s, set.ID, TermInput{Name: "Finance"})
	item, err := s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{keyword.ID}, Values: map[string]any{"topic": topic.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, item.Version); err != nil {
		t.Fatal(err)
	}
	if usage := inUse(t, s, topic.ID); usage != (TermUsage{Items: 1}) {
		t.Fatal("published usage", usage)
	}
	if _, err = s.Update(testContext, "alice", item.ID, latest(t, s, item.ID).Version, UpdateResource{Keywords: &[]string{}, Values: &map[string]any{"topic": nil}}); err != nil {
		t.Fatal(err)
	}
	// The published revision still holds both terms.
	if usage := inUse(t, s, keyword.ID); usage != (TermUsage{Items: 1}) {
		t.Fatal("published usage", usage)
	}
	if _, err = s.Publish(testContext, "alice", item.ID, latest(t, s, item.ID).Version); err != nil {
		t.Fatal(err)
	}
	deleteTerm(t, s, keyword.ID)
	deleteTerm(t, s, topic.ID)

	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	object, _ := exportWorkspaceForTest(t, s, store, ExportOptions{WorkspaceID: w.ID})
	target := emptyPackageTarget(t)
	imported, err := importWorkspaceForTest(t, target, store, object, false, "")
	if err != nil || imported.WorkspaceID == "" {
		t.Fatal("package with deleted terms in its history", imported, err)
	}
}
