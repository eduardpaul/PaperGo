package documentsearch

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/dms"
	"papergo/internal/model"
	"papergo/internal/testutil/searchstore"
	"papergo/internal/worker"
)

// fixture is a service with search registered, a workspace and a list.
func fixture(t *testing.T) (context.Context, *dms.Service, *Search, *resources.Resource) {
	t.Helper()
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	x := &Search{Store: searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})}
	if err := x.Register(s); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Invoices"})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, x, list
}

// indexAll writes every live item and folder, as the search.index workflow
// keeps them in a running PaperGo.
func indexAll(t *testing.T, s *dms.Service, x *Search) {
	t.Helper()
	ids, err := s.DAL.Resources().IDs(context.Background(), resources.Filter{Kinds: dms.ContentKinds, State: resources.Live})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.syncEntries(context.Background(), s, ids); err != nil {
		t.Fatal(err)
	}
}

func item(t *testing.T, s *dms.Service, parent, name string, values map[string]any) *resources.Resource {
	t.Helper()
	r, err := s.Create(context.Background(), "alice", parent, dms.CreateResource{Kind: "item", Name: name, Values: values})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Listings match their search text in the store, as a phrase whose last
// word is a prefix; while the store is rebuilt they match names.
func TestListingsMatchTextInTheStore(t *testing.T) {
	ctx, s, x, list := fixture(t)
	acme := item(t, s, list.ID, "Invoice INV-0000123 Acme Travel", nil)
	item(t, s, list.ID, "Invoice INV-0000456 Globex Legal", nil)
	indexAll(t, s, x)
	for search, want := range map[string]int{
		"acm":             1,
		"Acme tra":        1,
		"invoice":         2,
		"INV-0000":        2,
		"INV-0000123 acm": 1,
		"cme":             0,
		"travel acme":     0,
		`"acme`:           1,
		"---":             0,
		"*":               0,
	} {
		q, err := s.Query(ctx, "alice", list.ID, dms.QueryRequest{Query: dms.QuerySpec{Search: search}})
		if err != nil || len(q.Data) != want {
			t.Fatal("query search", search, len(q.Data), err)
		}
		page, err := s.Browse(ctx, "alice", dms.Browse{ParentID: list.ID, Search: search})
		if err != nil || len(page.Data) != want {
			t.Fatal("browse search", search, len(page.Data), err)
		}
		if want == 1 && q.Data[0].ID != acme.ID {
			t.Fatal("wrong match", search)
		}
	}
	x.rebuilding.Store(true)
	q, err := s.Query(ctx, "alice", list.ID, dms.QueryRequest{Query: dms.QuerySpec{Search: "cme"}})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != acme.ID {
		t.Fatal("names match while the store is rebuilt", q, err)
	}
	if _, err = x.Search(ctx, s, "alice", Request{Q: "acme"}); err != errUnavailable {
		t.Fatal("search while rebuilt", err)
	}
}

// Field text, keywords and term names, labels and synonyms are searchable
// text; a field whose search_weight is none is not.
func TestFieldsTermsAndKeywordsAreSearchable(t *testing.T) {
	ctx, s, x, list := fixture(t)
	for _, f := range []dms.CreateField{
		{Key: "notes", Label: "Notes", Type: "text"},
		{Key: "secret", Label: "Secret", Type: "text", Options: model.FieldOptions{SearchWeight: "none"}},
		{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}},
	} {
		if _, err := s.CreateField(ctx, "alice", list.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	group, err := s.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Classification"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	spain, err := s.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Spain", Labels: map[string]string{"es": "España"}, Synonyms: []string{"Iberia"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}}); err != nil {
		t.Fatal(err)
	}
	madrid := item(t, s, list.ID, "Madrid", map[string]any{"notes": "archival", "secret": "hidden", "keywords": []any{"Falcon"}, "region": spain.ID})
	item(t, s, list.ID, "Paris", nil)
	indexAll(t, s, x)
	for text, want := range map[string]int{"archival": 1, "hidden": 0, "falcon": 1, "spain": 1, "españa": 1, "iberia": 1} {
		q, err := s.Query(ctx, "alice", list.ID, dms.QueryRequest{Query: dms.QuerySpec{Search: text}})
		if err != nil || len(q.Data) != want || want == 1 && q.Data[0].ID != madrid.ID {
			t.Fatal(text, q, err)
		}
	}
	// Global search filters on the same fields.
	filter := &dms.FilterExpr{Field: "region", Op: "eq", Value: json.RawMessage(`"` + spain.ID + `"`)}
	found, err := x.Search(ctx, s, "alice", Request{Filter: filter, Kinds: []string{"item"}})
	if err != nil || len(found.Hits) != 1 || found.Hits[0].ID != madrid.ID {
		t.Fatal("field filter", found, err)
	}
}

// Global search finds entries by keyword, merged keywords by their target.
func TestGlobalSearchFindsKeywords(t *testing.T) {
	ctx, s, x, list := fixture(t)
	invoice, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{"Finance"}})
	if err != nil {
		t.Fatal(err)
	}
	finance := invoice.Keywords[0]
	target, _, err := s.AddKeyword(ctx, "alice", dms.KeywordInput{Name: "Accounts"})
	if err != nil {
		t.Fatal(err)
	}
	indexAll(t, s, x)
	found, err := x.Search(ctx, s, "alice", Request{Keyword: finance, Facets: true, Kinds: []string{"item"}})
	if err != nil || len(found.Hits) != 1 || found.Facets == nil || len(found.Facets.Keyword) != 1 {
		t.Fatal("keyword", found, err)
	}
	source, err := s.Term(ctx, "alice", finance)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.MergeTerm(ctx, "alice", finance, source.Version, dms.MergeTermInput{TargetTermID: target.ID}); err != nil {
		t.Fatal(err)
	}
	indexAll(t, s, x)
	found, err = x.Search(ctx, s, "alice", Request{Keyword: target.ID, Kinds: []string{"item"}})
	if err != nil || len(found.Hits) != 1 {
		t.Fatal("merged keyword", found, err)
	}
}

// Item keywords and fields with apply_parents give search their parent
// terms: as keywords, words and field matches; a move resyncs their holders.
func TestParentTermsAreSearchable(t *testing.T) {
	ctx, s, x, list := fixture(t)
	finance, err := s.CreateTerm(ctx, "alice", taxonomy.KeywordsSetID, dms.TermInput{Name: "Finance"})
	if err != nil {
		t.Fatal(err)
	}
	invoices, err := s.CreateTerm(ctx, "alice", taxonomy.KeywordsSetID, dms.TermInput{Name: "Invoices", ParentID: &finance.ID})
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Classification"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	europe, err := s.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Europe"})
	if err != nil {
		t.Fatal(err)
	}
	spain, err := s.CreateTerm(ctx, "alice", set.ID, dms.TermInput{Name: "Spain", ParentID: &europe.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []dms.CreateField{
		{Key: "places", Label: "Places", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID, ApplyParents: true}},
		{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}},
	} {
		if _, err = s.CreateField(ctx, "alice", list.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	tagged, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Tagged", Keywords: []string{invoices.ID}})
	if err != nil {
		t.Fatal(err)
	}
	madrid := item(t, s, list.ID, "Madrid", map[string]any{"places": spain.ID})
	item(t, s, list.ID, "Valencia", map[string]any{"region": spain.ID})
	indexAll(t, s, x)
	names := func(r Result) []string {
		out := []string{}
		for _, h := range r.Hits {
			out = append(out, h.Name)
		}
		slices.Sort(out)
		return out
	}
	found, err := x.Search(ctx, s, "alice", Request{Keyword: finance.ID, Kinds: []string{"item"}})
	if err != nil || !slices.Equal(names(found), []string{"Tagged"}) {
		t.Fatal("parent keyword", found, err)
	}
	if q, err := s.Query(ctx, "alice", list.ID, dms.QueryRequest{Query: dms.QuerySpec{Search: "europe"}}); err != nil || len(q.Data) != 1 || q.Data[0].ID != madrid.ID {
		t.Fatal("parent words", q, err)
	}
	for field, want := range map[string][]string{"places": {"Madrid"}, "region": {}} {
		filter := &dms.FilterExpr{Field: field, Op: "eq", Value: json.RawMessage(`"` + europe.ID + `"`)}
		found, err = x.Search(ctx, s, "alice", Request{Filter: filter, Kinds: []string{"item"}})
		if err != nil || !slices.Equal(names(found), want) {
			t.Fatal(field, "field filter", found, err)
		}
	}
	holders, err := Holders(ctx, s, "alice", list.WorkspaceID, "", []string{finance.ID}, "")
	if err != nil || !slices.Equal(holders.Items, []string{tagged.ID}) {
		t.Fatal("a move resyncs the holders of descendants", holders, err)
	}
}

// Queued syncs run together: one group writes every entry they name.
func TestSyncsRunTogether(t *testing.T) {
	ctx, s, x, list := fixture(t)
	var ids []string
	for _, name := range []string{"Acme", "Globex", "Initech"} {
		r := item(t, s, list.ID, name, nil)
		ids = append(ids, r.ID)
		if _, err := StartSync(ctx, s, "alice", r.WorkspaceID, []string{r.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if ran, err := s.RunOperationGroup(ctx, KindSync, "search", nil, 200); err != nil || !ran {
		t.Fatal(ran, err)
	}
	ops, err := s.DAL.Operations().List(ctx, operations.Filter{Kinds: []string{KindSync}})
	if err != nil || len(ops) != 3 || slices.ContainsFunc(ops, func(op operations.Operation) bool { return op.Status != operations.StatusSucceeded }) {
		t.Fatal("syncs", ops, err)
	}
	keys, err := x.Store.Keys(ctx, ids)
	if err != nil || len(keys) != 3 {
		t.Fatal("documents", keys, err)
	}
	if _, err = StartSync(ctx, s, "alice", list.WorkspaceID, nil); err == nil {
		t.Fatal("a sync without entries")
	}
}

// A rebuild writes every entry and removes documents of entries that are
// gone, and search is unavailable until it completes.
func TestRebuild(t *testing.T) {
	ctx, s, x, list := fixture(t)
	gone := item(t, s, list.ID, "Gone", nil)
	indexAll(t, s, x)
	if err := s.Delete(ctx, "alice", gone.ID, gone.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	kept := item(t, s, list.ID, "Kept", nil)
	raw, _ := json.Marshal(reindexPayload{Rebuild: true})
	if err := s.EnsureSystemOperation(ctx, KindReindex, raw); err != nil {
		t.Fatal(err)
	}
	if err := x.Start(ctx); err != nil || !x.rebuilding.Load() {
		t.Fatal("a pending rebuild marks search unavailable", err)
	}
	if err := worker.Drain(ctx, s); err != nil {
		t.Fatal(err)
	}
	keys, err := x.Store.Keys(ctx, []string{gone.ID, kept.ID})
	if err != nil || len(keys) != 1 || keys[0].ResourceID != kept.ID || x.rebuilding.Load() {
		t.Fatal(keys, err, x.rebuilding.Load())
	}
}

// Entries are indexed under the catalog type their list type copies: one
// facet counts a type across lists, hits name it, and filtering by a parent
// type finds its subtypes.
func TestSearchCountsContentTypesAcrossLists(t *testing.T) {
	ctx, s, x, list := fixture(t)
	record, err := s.CreateContentType(ctx, "alice", dms.ContentTypeInput{Key: "record", Name: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := s.CreateContentType(ctx, "alice", dms.ContentTypeInput{Key: "invoice", Name: "Invoice", ParentID: record.ID})
	if err != nil {
		t.Fatal(err)
	}
	// More lists than a facet lists values, each with one record.
	for i := range 25 {
		c, err := s.Create(ctx, "alice", *list.ParentID, dms.CreateResource{Kind: "list", Name: "Records " + strconv.Itoa(i), ContentTypeIDs: []string{record.ID}})
		if err != nil {
			t.Fatal(err)
		}
		item(t, s, c.ID, "Quarterly record", nil)
	}
	bills, err := s.Create(ctx, "alice", *list.ParentID, dms.CreateResource{Kind: "list", Name: "Bills", ContentTypeIDs: []string{invoice.ID}})
	if err != nil {
		t.Fatal(err)
	}
	item(t, s, bills.ID, "Quarterly bill", nil)
	indexAll(t, s, x)
	out, err := x.Search(ctx, s, "alice", Request{Q: "quarterly", Facets: true, Limit: 50})
	if err != nil || out.Facets == nil {
		t.Fatal(out, err)
	}
	counts := map[string]int{}
	for _, v := range out.Facets.ContentType {
		counts[v.Value] = v.Count
	}
	if counts[record.ID] != 25 || counts[invoice.ID] != 1 || len(counts) != 2 {
		t.Fatal("content type facet", out.Facets.ContentType)
	}
	if !slices.ContainsFunc(out.Hits, func(h Hit) bool { return h.ContentTypeID == invoice.ID }) {
		t.Fatal("hits name the catalog type", out.Hits)
	}
	out, err = x.Search(ctx, s, "alice", Request{Q: "quarterly", ContentTypeID: record.ID, Limit: 50})
	if err != nil || len(out.Hits) != 26 {
		t.Fatal("parent type filter", len(out.Hits), err)
	}
	out, err = x.Search(ctx, s, "alice", Request{Q: "quarterly", ContentTypeID: invoice.ID, Limit: 50})
	if err != nil || len(out.Hits) != 1 {
		t.Fatal("subtype filter", len(out.Hits), err)
	}
}
