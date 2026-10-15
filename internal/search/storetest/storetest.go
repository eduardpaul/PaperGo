// Package storetest is the conformance suite of search stores: every store
// runs it through search.Store only, so stores answer alike.
package storetest

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"papergo/internal/search"
)

var ctx = context.Background()

// Run runs the suite against fresh stores from open, which stem English.
func Run(t *testing.T, open func(t *testing.T) search.Store) {
	for name, test := range map[string]func(*testing.T, search.Store){
		"KeywordFindsNameKeywordsAndBody": keywordFindsNameKeywordsAndBody,
		"NameRanksAboveBody":              nameRanksAboveBody,
		"QuerySyntax":                     querySyntax,
		"AccessTrimsByScopeAndSurface":    accessTrimsByScopeAndSurface,
		"Filters":                         filters,
		"FiltersAloneListNewestFirst":     filtersAloneListNewestFirst,
		"PagingAndTotals":                 pagingAndTotals,
		"Facets":                          facets,
		"UpsertReplaces":                  upsertReplaces,
		"Deletes":                         deletes,
		"ScopeMoves":                      scopeMoves,
		"Match":                           match,
		"CaseAndDiacritics":               caseAndDiacritics,
		"Stemming":                        stemming,
		"FieldFilters":                    fieldFilters,
		"FieldFiltersAreExact":            fieldFiltersAreExact,
		"ContentIsSearchedOncePublished":  contentIsSearchedOncePublished,
		"ContentFollowsFileVersions":      contentFollowsFileVersions,
		"SemanticFindsContentByMeaning":   semanticFindsContentByMeaning,
		"HybridFusesBothLists":            hybridFusesBothLists,
		"VectorsAreReusedByInput":         vectorsAreReusedByInput,
		"Enrichments":                     enrichments,
	} {
		t.Run(name, func(t *testing.T) { test(t, open(t)) })
	}
}

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// doc is a published document readable in scope "s" of workspace "w" and
// collection "c", serving both views.
func doc(id, name string) search.Document {
	return search.Document{ResourceID: id, Kind: "item", Surface: search.SurfacePublished, Views: search.ViewHead | search.ViewPublished,
		WorkspaceID: "w", CollectionID: "c", ParentID: "c", ContentTypeID: "ct", ScopeID: "s", RevisionID: id + "-r1", Name: name,
		CreatedBy: "alice", CreatedAt: epoch, ModifiedBy: "alice", ModifiedAt: epoch}
}

var everyone = search.Access{Draft: []string{"s"}, Read: []string{"s"}}

func upsert(t *testing.T, s search.Store, docs ...search.Document) {
	t.Helper()
	if err := s.UpsertDocuments(ctx, docs, 1); err != nil {
		t.Fatal(err)
	}
}

func find(t *testing.T, s search.Store, text string, f search.Filter) []string {
	t.Helper()
	q := search.Query{Filter: f, Content: true, Limit: 100}
	if text != "" {
		var err error
		if q.Text, err = search.Parse(text); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Search(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, h := range res.Hits {
		ids = append(ids, h.ResourceID)
	}
	if res.Total != len(ids) {
		t.Fatalf("%q: total %d for %d hits", text, res.Total, len(ids))
	}
	return ids
}

func sorted(ids []string) []string {
	out := slices.Clone(ids)
	slices.Sort(out)
	return out
}

func expect(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !slices.Equal(sorted(got), sorted(want)) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func keywordFindsNameKeywordsAndBody(t *testing.T, s search.Store) {
	byName := doc("name", "Quarterly zephyr report")
	byKeywords := doc("keywords", "Tagged")
	byKeywords.Keywords = "zephyr"
	byBody := doc("body", "Notes")
	byBody.Body = "the zephyr was strong"
	upsert(t, s, byName, byKeywords, byBody, doc("none", "Unrelated"))
	expect(t, "zephyr", find(t, s, "zephyr", search.Filter{Access: everyone}), "name", "keywords", "body")
}

func nameRanksAboveBody(t *testing.T, s search.Store) {
	body := doc("body", "Notes")
	body.Body = "a long body that mentions harbor once among many other words of little interest"
	upsert(t, s, body, doc("name", "Harbor"))
	if got := find(t, s, "harbor", search.Filter{Access: everyone}); len(got) != 2 || got[0] != "name" {
		t.Fatalf("ranking: %v", got)
	}
}

func querySyntax(t *testing.T, s search.Store) {
	a, b, c := doc("a", "Late fee invoice"), doc("b", "Invoice draft"), doc("c", "Fee schedule")
	c.Body = "late payments"
	upsert(t, s, a, b, c)
	all := search.Filter{Access: everyone}
	expect(t, "phrase", find(t, s, `"late fee"`, all), "a")
	expect(t, "words", find(t, s, "late fee", all), "a", "c")
	expect(t, "or", find(t, s, "draft OR schedule", all), "b", "c")
	expect(t, "exclude", find(t, s, "invoice -draft", all), "a")
	expect(t, "not", find(t, s, "invoice NOT draft", all), "a")
	expect(t, "prefix", find(t, s, "invo*", all), "a", "b")
	expect(t, "phrase prefix", find(t, s, `"late fe"*`, all), "a")
}

func accessTrimsByScopeAndSurface(t *testing.T, s search.Store) {
	head := doc("a", "Alpha draft")
	head.Surface, head.Views, head.ScopeID = search.SurfaceHead, search.ViewHead, "s1"
	published := doc("a", "Alpha public")
	published.Views, published.ScopeID = search.ViewPublished, "s1"
	both := doc("b", "Alpha both")
	both.ScopeID = "s2"
	unscoped := doc("c", "Alpha loose")
	unscoped.ScopeID = ""
	upsert(t, s, head, published, both, unscoped)

	drafts := search.Access{Draft: []string{"s1"}, Read: []string{"s1", "s2"}}
	expect(t, "draft reader", find(t, s, "alpha", search.Filter{Access: drafts}), "a", "b")
	expect(t, "draft reader reads the head", find(t, s, "draft", search.Filter{Access: drafts}), "a")
	expect(t, "draft reader does not see the published copy", find(t, s, "public", search.Filter{Access: drafts}))
	readers := search.Access{Read: []string{"s1"}}
	expect(t, "reader reads published", find(t, s, "public", search.Filter{Access: readers}), "a")
	expect(t, "reader never sees drafts", find(t, s, "draft", search.Filter{Access: readers}))
	expect(t, "head surface", find(t, s, "alpha", search.Filter{Access: search.Access{Draft: []string{"s2"}}, Surface: search.SurfaceHead}), "b")
	expect(t, "published surface", find(t, s, "public", search.Filter{Access: drafts, Surface: search.SurfacePublished}), "a")
	expect(t, "head surface needs drafts", find(t, s, "alpha", search.Filter{Access: readers, Surface: search.SurfaceHead}))
	expect(t, "no scope", find(t, s, "alpha", search.Filter{Access: search.Access{Read: []string{""}}}), "c")
	expect(t, "nothing readable", find(t, s, "alpha", search.Filter{}))
}

func filters(t *testing.T, s search.Store) {
	a, b, f := doc("a", "Report one"), doc("b", "Report two"), doc("f", "Report folder")
	a.TermIDs, a.Tags, a.ModifiedBy = []string{"t1", "t2"}, []string{"red"}, "bob"
	b.WorkspaceID, b.CollectionID, b.ContentTypeID, b.CreatedBy, b.ModifiedAt = "w2", "c2", "ct2", "bob", epoch.Add(48*time.Hour)
	f.Kind = "folder"
	upsert(t, s, a, b, f)
	for name, tc := range map[string]struct {
		filter search.Filter
		want   []string
	}{
		"workspace":     {search.Filter{WorkspaceID: "w2"}, []string{"b"}},
		"collection":    {search.Filter{CollectionID: "c"}, []string{"a", "f"}},
		"content type":  {search.Filter{ContentTypeID: "ct2"}, []string{"b"}},
		"kind":          {search.Filter{Kinds: []string{"folder"}}, []string{"f"}},
		"terms":         {search.Filter{TermIDs: []string{"t2", "t9"}}, []string{"a"}},
		"tag":           {search.Filter{Tag: "red"}, []string{"a"}},
		"created by":    {search.Filter{CreatedBy: "bob"}, []string{"b"}},
		"modified by":   {search.Filter{ModifiedBy: "bob"}, []string{"a"}},
		"modified from": {search.Filter{ModifiedFrom: epoch.Add(time.Hour)}, []string{"b"}},
		"modified to":   {search.Filter{ModifiedTo: epoch.Add(time.Hour)}, []string{"a", "f"}},
	} {
		tc.filter.Access = everyone
		expect(t, name, find(t, s, "report", tc.filter), tc.want...)
	}
}

func filtersAloneListNewestFirst(t *testing.T, s search.Store) {
	old, recent := doc("old", "Old"), doc("new", "New")
	recent.ModifiedAt = epoch.Add(time.Hour)
	upsert(t, s, old, recent)
	if got := find(t, s, "", search.Filter{Access: everyone, CollectionID: "c"}); !slices.Equal(got, []string{"new", "old"}) {
		t.Fatalf("filters alone: %v", got)
	}
}

func pagingAndTotals(t *testing.T, s search.Store) {
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		upsert(t, s, doc(id, "Page "+id))
	}
	q, _ := search.Parse("page")
	var seen []string
	for offset := 0; offset < 5; offset += 2 {
		res, err := s.Search(ctx, search.Query{Text: q, Filter: search.Filter{Access: everyone}, Offset: offset, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if res.Total != 5 {
			t.Fatalf("total %d", res.Total)
		}
		for _, h := range res.Hits {
			seen = append(seen, h.ResourceID)
		}
	}
	expect(t, "pages", seen, "a", "b", "c", "d", "e")
}

func facets(t *testing.T, s search.Store) {
	a, b, c := doc("a", "Facet"), doc("b", "Facet"), doc("c", "Facet")
	a.TermIDs, b.TermIDs, a.Tags = []string{"t1"}, []string{"t1", "t2"}, []string{"red"}
	c.CollectionID, c.ContentTypeID = "c2", ""
	upsert(t, s, a, b, c)
	q, _ := search.Parse("facet")
	res, err := s.Search(ctx, search.Query{Text: q, Filter: search.Filter{Access: everyone}, Facets: true, Limit: 1})
	if err != nil || res.Facets == nil {
		t.Fatal(res, err)
	}
	f := res.Facets
	want := search.Facets{
		Workspace:   []search.FacetValue{{Value: "w", Count: 3}},
		Collection:  []search.FacetValue{{Value: "c", Count: 2}, {Value: "c2", Count: 1}},
		ContentType: []search.FacetValue{{Value: "ct", Count: 2}},
		Term:        []search.FacetValue{{Value: "t1", Count: 2}, {Value: "t2", Count: 1}},
		Tag:         []search.FacetValue{{Value: "red", Count: 1}},
	}
	if !slices.Equal(f.Workspace, want.Workspace) || !slices.Equal(f.Collection, want.Collection) || !slices.Equal(f.ContentType, want.ContentType) ||
		!slices.Equal(f.Term, want.Term) || !slices.Equal(f.Tag, want.Tag) {
		t.Fatalf("facets %+v, want %+v", *f, want)
	}
}

func upsertReplaces(t *testing.T, s search.Store) {
	a := doc("a", "Original title")
	a.TermIDs, a.Tags = []string{"t1"}, []string{"red"}
	upsert(t, s, a)
	a.Name, a.TermIDs, a.Tags = "Renamed", []string{"t2"}, nil
	upsert(t, s, a)
	all := search.Filter{Access: everyone}
	expect(t, "old text", find(t, s, "original", all))
	expect(t, "new text", find(t, s, "renamed", all), "a")
	expect(t, "old term", find(t, s, "", search.Filter{Access: everyone, TermIDs: []string{"t1"}}))
	expect(t, "new term", find(t, s, "", search.Filter{Access: everyone, TermIDs: []string{"t2"}}), "a")
	expect(t, "old tag", find(t, s, "", search.Filter{Access: everyone, Tag: "red"}))
}

func deletes(t *testing.T, s search.Store) {
	head := doc("a", "Delete me")
	head.Surface = search.SurfaceHead
	upsert(t, s, doc("a", "Delete me"), head, doc("b", "Delete me"), doc("c", "Delete me"))
	if err := s.UpsertDocuments(ctx, []search.Document{doc("d", "Delete me")}, 2); err != nil {
		t.Fatal(err)
	}
	keys, err := s.Keys(ctx, []string{"a", "x"})
	if err != nil || len(keys) != 2 {
		t.Fatalf("keys: %v %v", keys, err)
	}
	if err = s.DeleteDocuments(ctx, search.Selection{Keys: []search.Key{{ResourceID: "a", Surface: search.SurfaceHead}}}); err != nil {
		t.Fatal(err)
	}
	if keys, _ = s.Keys(ctx, []string{"a"}); len(keys) != 1 || keys[0].Surface != search.SurfacePublished {
		t.Fatalf("after deleting the head: %v", keys)
	}
	if err = s.DeleteDocuments(ctx, search.Selection{ResourceIDs: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	expect(t, "after deleting entries", find(t, s, "delete", search.Filter{Access: everyone}), "c", "d")
	if err = s.DeleteDocuments(ctx, search.Selection{Before: 2}); err != nil {
		t.Fatal(err)
	}
	expect(t, "after deleting older stamps", find(t, s, "delete", search.Filter{Access: everyone}), "d")
}

func scopeMoves(t *testing.T, s search.Store) {
	upsert(t, s, doc("a", "Moving"), doc("b", "Moving"))
	if err := s.SetScopes(ctx, map[string]string{"a": "private", "x": "private"}); err != nil {
		t.Fatal(err)
	}
	expect(t, "old scope", find(t, s, "moving", search.Filter{Access: everyone}), "b")
	expect(t, "new scope", find(t, s, "moving", search.Filter{Access: search.Access{Read: []string{"private"}}}), "a")
}

func match(t *testing.T, s search.Store) {
	head := doc("a", "Acme draft")
	head.Surface, head.Views = search.SurfaceHead, search.ViewHead
	published := doc("a", "Acme")
	published.Views = search.ViewPublished
	other := doc("o", "Acme elsewhere")
	other.CollectionID = "c2"
	upsert(t, s, head, published, doc("b", "Acme corp"), doc("z", "Zeta"), other)
	m, err := s.Match(ctx, search.MatchQuery{CollectionID: "c", Text: search.Literal("acm")})
	if err != nil || m.Capped {
		t.Fatal(m, err)
	}
	expect(t, "head matches", m.Head, "a", "b")
	expect(t, "published matches", m.Published, "a", "b")
	if m, err = s.Match(ctx, search.MatchQuery{CollectionID: "c", Text: search.Literal("acme dra")}); err != nil {
		t.Fatal(err)
	}
	expect(t, "head phrase", m.Head, "a")
	expect(t, "published phrase", m.Published)
	if m, err = s.Match(ctx, search.MatchQuery{CollectionID: "c", Text: search.Literal("acme"), Limit: 1}); err != nil || !m.Capped {
		t.Fatalf("cap: %+v %v", m, err)
	}
	if m, err = s.Match(ctx, search.MatchQuery{WorkspaceID: "w", Text: search.Literal("acme")}); err != nil {
		t.Fatal(err)
	}
	expect(t, "workspace", m.Published, "a", "b", "o")
}

func caseAndDiacritics(t *testing.T, s search.Store) {
	upsert(t, s, doc("a", "Café Ünïcode"))
	all := search.Filter{Access: everyone}
	expect(t, "folded", find(t, s, "cafe UNICODE", all), "a")
	expect(t, "accented", find(t, s, "café", all), "a")
}

// A word finds its other forms by their stems; words that match as they are
// rank first, prefixes match words only, and collection matching is literal.
func stemming(t *testing.T, s search.Store) {
	exact, plural, other := doc("a", "Invoice"), doc("b", "Invoices"), doc("c", "Studies")
	plural.Body = "Paid invoices of March"
	upsert(t, s, exact, plural, other)
	all := search.Filter{Access: everyone}
	expect(t, "singular finds plural", sorted(find(t, s, "invoice", all)), "a", "b")
	expect(t, "plural finds singular", sorted(find(t, s, "invoices", all)), "a", "b")
	if got := find(t, s, "invoice", all); got[0] != "a" {
		t.Fatalf("the exact word ranks first: %v", got)
	}
	expect(t, "stemmed phrase", find(t, s, `"paid invoice"`, all), "b")
	expect(t, "stemmed exclusion", find(t, s, "invoice -invoices", all))
	expect(t, "study", find(t, s, "study", all), "c")
	expect(t, "prefix is literal", find(t, s, "study*", all))
	m, err := s.Match(ctx, search.MatchQuery{CollectionID: "c", Text: search.Literal("study")})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "collection match is literal", m.Published)
}

func withFields(id string, fields ...search.Field) search.Document {
	d := doc(id, "Record "+id)
	d.Fields = fields
	return d
}

func text(key string, values ...string) search.Field {
	f := search.Field{Key: key, Type: "text"}
	for _, v := range values {
		f.Values = append(f.Values, search.FieldValue{Text: v})
	}
	return f
}

func integer(key string, values ...int64) search.Field {
	f := search.Field{Key: key, Type: "integer"}
	for _, v := range values {
		f.Values = append(f.Values, search.FieldValue{Int: v})
	}
	return f
}

func cond(key, typ, op string, values ...search.FieldValue) search.FieldFilter {
	return search.FieldFilter{Key: key, Type: typ, Op: op, Values: values}
}

func filterFields(t *testing.T, s search.Store, f search.FieldFilter) []string {
	t.Helper()
	return sorted(find(t, s, "", search.Filter{Access: everyone, Fields: &f}))
}

func fieldFilters(t *testing.T, s search.Store) {
	upsert(t, s,
		withFields("a", text("status", "open"), integer("amount", 10), text("labels", "x", "y")),
		withFields("b", text("status", "closed"), integer("amount", 20), text("labels", "y")),
		withFields("c", integer("amount", 30)),
		// The same key with another type is another field.
		withFields("d", search.Field{Key: "status", Type: "choice", Values: []search.FieldValue{{Text: "open"}}}))
	n := func(v int64) search.FieldValue { return search.FieldValue{Int: v} }
	str := func(v string) search.FieldValue { return search.FieldValue{Text: v} }
	expect(t, "eq", filterFields(t, s, cond("status", "text", "eq", str("open"))), "a")
	expect(t, "other type", filterFields(t, s, cond("status", "choice", "eq", str("open"))), "d")
	expect(t, "gt", filterFields(t, s, cond("amount", "integer", "gt", n(10))), "b", "c")
	expect(t, "gte", filterFields(t, s, cond("amount", "integer", "gte", n(20))), "b", "c")
	expect(t, "lt", filterFields(t, s, cond("amount", "integer", "lt", n(20))), "a")
	expect(t, "lte", filterFields(t, s, cond("amount", "integer", "lte", n(20))), "a", "b")
	expect(t, "in", filterFields(t, s, cond("amount", "integer", "in", n(10), n(30), n(99))), "a", "c")
	expect(t, "ne needs the field", filterFields(t, s, cond("status", "text", "ne", str("open"))), "b")
	expect(t, "ne on a multi-value", filterFields(t, s, cond("labels", "text", "ne", str("x"))), "b")
	expect(t, "present", filterFields(t, s, cond("labels", "text", "present")), "a", "b")
	expect(t, "missing", filterFields(t, s, cond("labels", "text", "missing")), "c", "d")
	expect(t, "and", filterFields(t, s, search.FieldFilter{And: []search.FieldFilter{cond("labels", "text", "eq", str("y")), cond("amount", "integer", "gt", n(10))}}), "b")
	expect(t, "or", filterFields(t, s, search.FieldFilter{Or: []search.FieldFilter{cond("status", "text", "eq", str("open")), cond("amount", "integer", "eq", n(30))}}), "a", "c")
	not := cond("amount", "integer", "eq", n(10))
	expect(t, "not", filterFields(t, s, search.FieldFilter{Not: &not}), "b", "c", "d")
	// Replacing a document replaces its fields.
	upsert(t, s, withFields("a", integer("amount", 31)))
	expect(t, "replaced", filterFields(t, s, cond("amount", "integer", "gt", n(30))), "a")
	expect(t, "old values gone", filterFields(t, s, cond("status", "text", "eq", str("open"))))
	// Text and filters combine.
	if got := find(t, s, "record", search.Filter{Access: everyone, Fields: &search.FieldFilter{Key: "amount", Type: "integer", Op: "lt", Values: []search.FieldValue{n(25)}}}); len(got) != 1 || got[0] != "b" {
		t.Fatalf("text and filter: %v", got)
	}
}

// Values compare exactly: integers past float precision, decimals by their
// units at a scale, and fields of another scale apart.
func fieldFiltersAreExact(t *testing.T, s search.Store) {
	big := func(id string, v int64) search.Document { return withFields(id, integer("serial", v)) }
	decimal := func(id string, units int64, scale int) search.Document {
		return withFields(id, search.Field{Key: "rate", Type: "decimal", Scale: scale, Values: []search.FieldValue{{Int: units}}})
	}
	upsert(t, s, big("a", 9007199254740993), big("b", 9007199254740992), decimal("c", 1_000000001, 9), decimal("d", 1_000000000, 9), decimal("e", 1, 0))
	expect(t, "big eq", filterFields(t, s, cond("serial", "integer", "eq", search.FieldValue{Int: 9007199254740993})), "a")
	expect(t, "big gt", filterFields(t, s, cond("serial", "integer", "gt", search.FieldValue{Int: 9007199254740992})), "a")
	scaled := search.FieldFilter{Key: "rate", Type: "decimal", Scale: 9, Op: "gt", Values: []search.FieldValue{{Int: 1_000000000}}}
	expect(t, "decimal units", filterFields(t, s, scaled), "c")
	scaled.Scale, scaled.Op, scaled.Values = 0, "eq", []search.FieldValue{{Int: 1}}
	expect(t, "another scale", filterFields(t, s, scaled), "e")
}

func withFile(id, name, blob string) search.Document {
	d := doc(id, name)
	d.BlobID = blob
	return d
}

func chunks(part string, pages ...string) []search.Chunk {
	out := make([]search.Chunk, len(pages))
	for i, text := range pages {
		out[i] = search.Chunk{Part: part, Ordinal: i, Page: i + 1, Kind: search.KindContent, Text: text}
	}
	return out
}

func stage(t *testing.T, s search.Store, item, blob, generation string, c []search.Chunk) {
	t.Helper()
	if err := s.StageContent(ctx, item, blob, generation, c); err != nil {
		t.Fatal(err)
	}
}

func publish(t *testing.T, s search.Store, item, blob, generation string) {
	t.Helper()
	if err := s.PublishContent(ctx, item, blob, generation); err != nil {
		t.Fatal(err)
	}
}

func hitOf(t *testing.T, s search.Store, text string) search.Hit {
	t.Helper()
	q, err := search.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Search(ctx, search.Query{Text: q, Filter: search.Filter{Access: everyone}, Content: true, Limit: 10})
	if err != nil || len(res.Hits) != 1 {
		t.Fatalf("%q: %+v %v", text, res.Hits, err)
	}
	return res.Hits[0]
}

// File text is searched once published: the page and snippet come from the
// best matching chunk, and collection matches see it too.
func contentIsSearchedOncePublished(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "Contract", "b1"), withFile("x", "Other", "bx"))
	stage(t, s, "a", "b1", "g1", chunks("read", "Parties and scope", "Payment is due within thirty days of invoices"))
	all := search.Filter{Access: everyone}
	expect(t, "staged is invisible", find(t, s, "payment", all))
	if c, err := s.Content(ctx, "a", "b1"); err != nil || c.Generation != "" {
		t.Fatalf("content before publishing: %+v %v", c, err)
	}
	publish(t, s, "a", "b1", "g1")
	h := hitOf(t, s, "payment")
	if h.ResourceID != "a" || h.Page != 2 || !strings.Contains(h.Text, "thirty days") {
		t.Fatalf("page hit: %+v", h)
	}
	if h = hitOf(t, s, "invoice"); h.Page != 2 {
		t.Fatalf("stemmed page hit: %+v", h)
	}
	if h = hitOf(t, s, "contract"); h.Page != 0 {
		t.Fatalf("a name match is not on a page: %+v", h)
	}
	m, err := s.Match(ctx, search.MatchQuery{CollectionID: "c", Text: search.Literal("thirty da"), Content: true})
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "collection match", m.Published, "a")
	if c, err := s.Content(ctx, "a", "b1"); err != nil || c.Generation != "g1" || c.Chunks != 2 {
		t.Fatalf("content: %+v %v", c, err)
	}
	if st, err := s.Stats(ctx); err != nil || st.Chunks != 2 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	// A newer generation replaces it; metadata writes keep it.
	stage(t, s, "a", "b1", "g2", chunks("read", "Termination needs notice"))
	expect(t, "old content until published", find(t, s, "payment", all), "a")
	publish(t, s, "a", "b1", "g2")
	expect(t, "old generation gone", find(t, s, "payment", all))
	upsert(t, s, withFile("a", "Renamed", "b1"))
	expect(t, "content survives a metadata write", find(t, s, "termination", all), "a")
	// A part added to a published generation is searched at once.
	stage(t, s, "a", "b1", "g2", []search.Chunk{{Part: "summary", Kind: search.KindSummary, Text: "Short summary about leasing"}})
	expect(t, "appended part", find(t, s, "leasing", all), "a")
	expect(t, "first part kept", find(t, s, "termination", all), "a")
	// Staging a part again replaces its chunks only.
	stage(t, s, "a", "b1", "g2", []search.Chunk{{Part: "summary", Kind: search.KindSummary, Text: "Short summary about renting"}})
	expect(t, "restaged part", find(t, s, "leasing", all))
	expect(t, "restaged part text", find(t, s, "renting", all), "a")
}

// Content belongs to a file version: a new version starts without it, and
// dropped versions and entries take theirs along.
func contentFollowsFileVersions(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "Scan", "b1"))
	stage(t, s, "a", "b1", "g1", chunks("read", "Original receipt"))
	publish(t, s, "a", "b1", "g1")
	all := search.Filter{Access: everyone}
	upsert(t, s, withFile("a", "Scan", "b2"))
	expect(t, "a new file version has no content yet", find(t, s, "receipt", all))
	if err := s.DeleteContent(ctx, "a", []string{"b2"}); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Content(ctx, "a", "b1"); err != nil || c.Chunks != 0 {
		t.Fatalf("content of a dropped version: %+v %v", c, err)
	}
	stage(t, s, "a", "b2", "g2", chunks("read", "Corrected receipt"))
	publish(t, s, "a", "b2", "g2")
	expect(t, "new version content", find(t, s, "corrected", all), "a")
	if err := s.DeleteDocuments(ctx, search.Selection{ResourceIDs: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Content(ctx, "a", "b2"); err != nil || c.Chunks != 0 {
		t.Fatalf("content of a deleted entry: %+v %v", c, err)
	}
	// A reindex drops content of entries it did not write.
	upsert(t, s, withFile("z", "Old", "bz"))
	stage(t, s, "z", "bz", "gz", chunks("read", "Stale"))
	publish(t, s, "z", "bz", "gz")
	if err := s.DeleteDocuments(ctx, search.Selection{Before: 2}); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Content(ctx, "z", "bz"); err != nil || c.Chunks != 0 {
		t.Fatalf("content after a reindex: %+v %v", c, err)
	}
}

// Enrichments add model-written context to chunks: it is searched with the
// chunk, changes the chunk's input (so it embeds again), and applies at once
// to published content.
func enrichments(t *testing.T, s search.Store) {
	upsert(t, s, withFile("a", "Lease", "b1"))
	stage(t, s, "a", "b1", "g1", chunks("read", "The tenant pays monthly", "Either party may end it"))
	before, _, err := s.GenerationToEmbed(ctx, "m", "g1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetEnrichments(ctx, "g1", []search.Enrichment{{Part: "read", Ordinal: 1, Text: "Termination clause of the lease"}, {Part: "read", Ordinal: 9, Text: "no such chunk"}}); err != nil {
		t.Fatal(err)
	}
	staged, err := s.GenerationChunks(ctx, "g1")
	if err != nil || len(staged) != 2 || staged[0].Enrichment != "" || staged[1].Enrichment != "Termination clause of the lease" || staged[1].Page != 2 || staged[1].Text != "Either party may end it" {
		t.Fatalf("chunks: %+v %v", staged, err)
	}
	after, _, err := s.GenerationToEmbed(ctx, "m", "g1")
	if err != nil || len(after) != 2 || after[1].Input != "Either party may end it\nTermination clause of the lease" || after[1].InputHash == before[1].InputHash || after[0].InputHash != before[0].InputHash {
		t.Fatalf("inputs: %+v (was %+v) %v", after, before, err)
	}
	publish(t, s, "a", "b1", "g1")
	if h := hitOf(t, s, "termination"); h.ResourceID != "a" || h.Page != 2 {
		t.Fatalf("enrichment hit: %+v", h)
	}
	if err = s.SetEnrichments(ctx, "g1", []search.Enrichment{{Part: "read", Ordinal: 0, Text: "Rent payment terms"}}); err != nil {
		t.Fatal(err)
	}
	if h := hitOf(t, s, "rent"); h.Page != 1 {
		t.Fatalf("published enrichment: %+v", h)
	}
}
