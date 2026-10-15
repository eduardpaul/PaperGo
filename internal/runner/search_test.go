package runner

import (
	"errors"
	"slices"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/workflow"
)

// searchNames waits until the subject's search for q finds exactly want
// (names, in any order), as the search.index built-in catches up.
func (f *fixture) searchNames(t *testing.T, subject string, in dms.SearchRequest, want ...string) {
	t.Helper()
	slices.Sort(want)
	deadline := time.Now().Add(20 * time.Second)
	for {
		out, err := f.dms.Search(ctx, subject, in)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, h := range out.Hits {
			got = append(got, h.Name)
		}
		slices.Sort(got)
		// A settled index holds nothing the subject may not see.
		if slices.Equal(got, want) && !out.TotalEstimated {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("search %q as %s: %v (estimated %v), want %v", in.Q, subject, got, out.TotalEstimated, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The default search.index built-in keeps the store in step with items,
// folders, permissions and terms, and a collection can turn it off.
func TestSearchIndexBuiltInKeepsTheStoreCurrent(t *testing.T) {
	f := setup(t)
	group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Corporate"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	finance, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	accounting, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Accounting"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.CreateField(ctx, "alice", f.list.ID, dms.CreateField{Key: "topic", Label: "Topic", Type: "term", Options: model.FieldOptions{TermSetID: set.ID}}); err != nil {
		t.Fatal(err)
	}
	item := f.item(t, "alice", "Quarterly plan", map[string]any{"topic": finance.ID})
	head := dms.SearchRequest{Q: "quarterly", Surface: "head"}
	f.searchNames(t, "alice", head, "Quarterly plan")
	f.searchNames(t, "alice", dms.SearchRequest{Q: "finance", Surface: "head"}, "Quarterly plan")

	// Renaming the item and the term reindexes what carries them.
	name := "Annual plan"
	current := f.head(t, item.ID)
	if _, err = f.dms.Update(ctx, "alice", item.ID, current.Version, dms.UpdateResource{Name: &name}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", head)
	f.searchNames(t, "alice", dms.SearchRequest{Q: "annual", Surface: "head"}, "Annual plan")
	if _, err = f.dms.UpdateTerm(ctx, "alice", finance.ID, finance.Version, dms.TermInput{Name: "Treasury"}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "treasury", Surface: "head"}, "Annual plan")
	f.searchNames(t, "alice", dms.SearchRequest{Q: "finance", Surface: "head"})
	// A merge moves the item to the target's words, and its term filter
	// finds the item by the merged term too.
	source, err := f.dms.Term(ctx, "alice", finance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.MergeTerm(ctx, "alice", finance.ID, source.Version, dms.MergeTermInput{TargetTermID: accounting.ID}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "accounting", Surface: "head"}, "Annual plan")
	f.searchNames(t, "alice", dms.SearchRequest{TermID: finance.ID, Surface: "head"}, "Annual plan")

	// Folders are indexed and their deletion removes them.
	f.folder(t, f.list.ID, "Receipts")
	folders := dms.SearchRequest{Q: "receipts", Kinds: []string{"folder"}}
	f.searchNames(t, "alice", folders, "Receipts")

	// Unique permissions reach the documents of every item in the scope.
	f.searchNames(t, "bob", dms.SearchRequest{Q: "annual", Surface: "head"}, "Annual plan")
	list := f.head(t, f.list.ID)
	owners := []dms.Permission{{PrincipalType: "workspace_owner", PrincipalID: f.ws.ID, Action: "manage", Effect: "allow"}}
	if _, err = f.dms.SetPermissions(ctx, "alice", f.list.ID, list.Version, dms.Permissions{Grants: owners}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "bob", dms.SearchRequest{Q: "annual", Surface: "head"})
	f.searchNames(t, "alice", dms.SearchRequest{Q: "annual", Surface: "head"}, "Annual plan")

	current = f.head(t, item.ID)
	if err = f.dms.Delete(ctx, "alice", item.ID, current.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitKeys(t, f, item.ID, 0)

	// Turned off for a collection, its changes start no indexing runs.
	all, err := f.wf.List(ctx, "alice", f.ws.ID)
	if err != nil {
		t.Fatal(err)
	}
	var index workflow.Workflow
	for _, w := range all {
		if w.BuiltInKey != nil && *w.BuiltInKey == workflow.RoleSearchIndex {
			index = w
		}
	}
	if index.ID == "" || !index.Enabled || index.CollectionID != nil {
		t.Fatalf("default built-in: %+v", index)
	}
	if _, err = f.wf.UpdateBuiltIn(ctx, "alice", f.ws.ID, workflow.RoleSearchIndex, workflow.SetBuiltIn{CollectionID: f.list.ID, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	// Its documents would go stale, so searching inside it says so.
	if _, err = f.dms.Browse(ctx, "alice", dms.Browse{ParentID: f.list.ID, Search: "plan"}); !errors.Is(err, dms.ErrSearchOff) {
		t.Fatalf("search in a collection without search.index: %v", err)
	}
	quiet := f.item(t, "alice", "Unindexed note", nil)
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: index.ID, ItemID: quiet.ID})
	if err != nil || len(page.Data) != 0 {
		t.Fatalf("indexed a collection that turned search.index off: %+v %v", page.Data, err)
	}
	// System runs stay out of listings unless asked for by workflow.
	page, err = f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range page.Data {
		if run.WorkflowID == index.ID {
			t.Fatalf("system run listed: %+v", run)
		}
	}
	if page, err = f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: index.ID}); err != nil || len(page.Data) == 0 {
		t.Fatalf("system runs by workflow: %v %v", len(page.Data), err)
	}
}

func waitKeys(t *testing.T, f *fixture, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		keys, err := f.dms.SearchStore().Keys(ctx, []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("documents of %s: %v, want %d", id, keys, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSearchRefreshesAliasesOfRenamedAndMergedTerms(t *testing.T) {
	f := setup(t)
	group, err := f.dms.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Group"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.dms.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Accounting"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.CreateField(ctx, "alice", f.list.ID, dms.CreateField{Key: "topic", Label: "Topic", Type: "term", Options: model.FieldOptions{TermSetID: set.ID}}); err != nil {
		t.Fatal(err)
	}
	f.item(t, "alice", "Quarterly report", map[string]any{"topic": source.ID})
	if target, err = f.dms.MergeTerm(ctx, "alice", source.ID, source.Version, dms.MergeTermInput{TargetTermID: target.ID}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "Finance"}, "Quarterly report")
	// Finish earlier syncs before relabeling: a merge run reading after the
	// rename could otherwise mask a missed term.changed delivery.
	if _, err = f.r.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	index := f.builtIn(t, "search.index", "")
	page, err := f.r.Runs(ctx, "alice", f.ws.ID, RunFilter{WorkflowID: index.ID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	f.runs(t, index.ID, len(page.Data))
	if target, err = f.dms.UpdateTerm(ctx, "alice", target.ID, target.Version, dms.TermInput{Name: "Treasury"}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "Treasury"}, "Quarterly report")
	final, err := f.dms.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Planning"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.dms.MergeTerm(ctx, "alice", target.ID, target.Version, dms.MergeTermInput{TargetTermID: final.ID}); err != nil {
		t.Fatal(err)
	}
	f.searchNames(t, "alice", dms.SearchRequest{Q: "Planning"}, "Quarterly report")
}
