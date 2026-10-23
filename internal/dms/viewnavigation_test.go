package dms

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"papergo/internal/dal/taxonomy"
	viewstore "papergo/internal/dal/views"
	"papergo/internal/model"
)

func TestNavigationViews(t *testing.T) {
	s, _, l := fixture(t)
	periods, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "periods", Name: "Periods"})
	if err != nil {
		t.Fatal(err)
	}
	fiscal := mustTerm(t, s, periods.ID, TermInput{Name: "Fiscal"})
	q1 := mustTerm(t, s, periods.ID, TermInput{Name: "Q1", ParentID: &fiscal.ID})
	mustTerm(t, s, periods.ID, TermInput{Name: "Q2", ParentID: &fiscal.ID})
	mustTerm(t, s, periods.ID, TermInput{Name: "Calendar"})
	finance := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Finance"})
	invoices := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Invoices", ParentID: &finance.ID})
	for _, f := range []CreateField{
		{Key: "period", Label: "Period", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: periods.ID}},
		{Key: "periods", Label: "Periods", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: periods.ID, Multiple: true, ApplyParents: true}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, in := range []CreateResource{
		{Name: "Budget", Values: map[string]any{"period": fiscal.ID, "periods": []any{fiscal.ID}}, Keywords: []string{invoices.ID}},
		{Name: "Report", Values: map[string]any{"period": q1.ID, "periods": []any{q1.ID}}, Keywords: []string{finance.ID}},
		{Name: "Memo", Keywords: []string{invoices.ID}},
		{Name: "Loose"},
	} {
		in.Kind = "item"
		if _, err = s.Create(testContext, "alice", l.ID, in); err != nil {
			t.Fatal(in.Name, err)
		}
	}
	if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "folder", Name: "Archive"}); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	fields := func(keys ...string) []NavigationFieldSetting {
		out := []NavigationFieldSetting{}
		for _, key := range keys {
			out = append(out, NavigationFieldSetting{Field: key})
		}
		return out
	}
	view := func(name string, settings ViewSettings) *viewstore.ListView {
		t.Helper()
		v, err := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: name, Layout: viewstore.LayoutNavigation, Settings: settings})
		if err != nil {
			t.Fatal(name, err)
		}
		return v
	}
	for _, bad := range []ViewSettings{
		{},
		{Fields: fields("missing")},
		{Fields: fields("period", "period")},
		{Fields: fields("period", "periods", "$keywords", "period", "periods", "$keywords")},
		{Fields: fields("period"), Match: "some"},
		{Fields: []NavigationFieldSetting{{Field: "periods", IncludeSubterms: &no}}},
		{Fields: []NavigationFieldSetting{{Field: "$keywords", IncludeSubterms: &no}}},
	} {
		if _, err = s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Bad", Layout: viewstore.LayoutNavigation, Settings: bad}); err == nil {
			t.Fatal("invalid settings", bad)
		}
	}
	if _, err = s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Bad", Settings: ViewSettings{Fields: fields("period")}}); err == nil {
		t.Fatal("settings on a table view")
	}
	level := func(v *viewstore.ListView, field, parent string, selected ...NavigationSelection) (string, *int) {
		t.Helper()
		nav, err := s.ViewNavigation(testContext, "alice", v.ID, NavigationRequest{Field: field, ParentID: parent, Selected: selected})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range nav.Data {
			children := ""
			if n.HasChildren {
				children = "+"
			}
			out = append(out, n.Name+children+":"+strconv.Itoa(n.Count)+"/"+strconv.Itoa(n.SubtreeCount))
		}
		return strings.Join(out, ","), nav.NoValueCount
	}
	items := func(v *viewstore.ListView, selected ...NavigationSelection) string {
		t.Helper()
		page, err := s.QueryView(testContext, "alice", v.ID, ViewQueryRequest{Selected: selected})
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range page.Data {
			names = append(names, r.Name)
		}
		slices.Sort(names)
		return strings.Join(names, ",")
	}
	term := func(field, id string) NavigationSelection { return NavigationSelection{Field: field, TermID: id} }
	none := func(field string) NavigationSelection { return NavigationSelection{Field: field, NoValue: true} }

	// A field without apply_parents lists the items holding the term; the
	// view's keywords apply their parents.
	all := view("All", ViewSettings{Fields: fields("period", "$keywords")})
	if got, n := level(all, "period", ""); got != "Calendar:0/0,Fiscal+:1/2" || n == nil || *n != 2 {
		t.Fatal("period roots", got, n)
	}
	if got, _ := level(all, "period", fiscal.ID); got != "Q1:1/1,Q2:0/0" {
		t.Fatal("period children", got)
	}
	if got, n := level(all, "$keywords", ""); got != "Finance+:3/3" || *n != 1 {
		t.Fatal("keyword roots", got, *n)
	}
	if got := items(all, term("period", fiscal.ID)); got != "Budget" {
		t.Fatal("exact items", got)
	}
	// No value lists items only, not folders.
	if got := items(all, none("period")); got != "Loose,Memo" {
		t.Fatal("no value", got)
	}

	// Matching all, a field's counts follow the terms opened in the others.
	if got, n := level(all, "$keywords", "", term("period", fiscal.ID)); got != "Finance+:1/1" || *n != 0 {
		t.Fatal("narrowed keyword roots", got, *n)
	}
	if got, _ := level(all, "period", "", term("$keywords", invoices.ID), term("period", q1.ID)); got != "Calendar:0/0,Fiscal+:1/1" {
		t.Fatal("a field's own selection does not narrow it", got)
	}
	if got := items(all, term("period", fiscal.ID), term("$keywords", finance.ID)); got != "Budget" {
		t.Fatal("all items", got)
	}
	if got := items(all, none("period"), term("$keywords", finance.ID)); got != "Memo" {
		t.Fatal("no value and a term", got)
	}

	// Matching any, the opened terms add items and counts cover the view.
	anyOf := view("Any", ViewSettings{Fields: fields("period", "$keywords"), Match: MatchAny})
	if got := items(anyOf, term("period", q1.ID), term("$keywords", invoices.ID)); got != "Budget,Memo,Report" {
		t.Fatal("any items", got)
	}
	if got, _ := level(anyOf, "$keywords", "", term("period", fiscal.ID)); got != "Finance+:3/3" {
		t.Fatal("any counts", got)
	}

	// The view can include subterms per field; hiding empty terms drops
	// Calendar and Q2.
	rolled := view("Rolled", ViewSettings{Fields: []NavigationFieldSetting{{Field: "period", IncludeSubterms: &yes}}, HideEmpty: true})
	if got, _ := level(rolled, "period", ""); got != "Fiscal+:2/2" {
		t.Fatal("rolled roots", got)
	}
	if got := items(rolled, term("period", fiscal.ID)); got != "Budget,Report" {
		t.Fatal("rolled items", got)
	}
	// A field that applies parents always includes subterms.
	applied := view("Applied", ViewSettings{Fields: fields("periods"), HideEmpty: true})
	if got, _ := level(applied, "periods", fiscal.ID); got != "Q1:1/1" {
		t.Fatal("applied children", got)
	}
	if got := items(applied, term("periods", fiscal.ID)); got != "Budget,Report" {
		t.Fatal("applied items", got)
	}

	for _, bad := range [][]NavigationSelection{
		nil,
		{term("periods", fiscal.ID)},
		{term("period", fiscal.ID), term("period", q1.ID)},
		{{Field: "period"}},
		{{Field: "period", TermID: fiscal.ID, NoValue: true}},
	} {
		if _, err = s.QueryView(testContext, "alice", all.ID, ViewQueryRequest{Selected: bad}); err == nil {
			t.Fatal("invalid selection", bad)
		}
	}
	if _, err = s.ViewNavigation(testContext, "alice", all.ID, NavigationRequest{Field: "periods"}); err == nil {
		t.Fatal("a field the view does not browse")
	}
	if _, err = s.ViewNavigation(testContext, "alice", all.ID, NavigationRequest{Field: "period", ParentID: finance.ID}); err == nil {
		t.Fatal("parent from another set")
	}
}
