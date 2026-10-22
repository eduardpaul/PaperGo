package dms

import (
	"slices"
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
	for _, f := range []CreateField{
		{Key: "period", Label: "Period", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: periods.ID}},
		{Key: "periods", Label: "Periods", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: periods.ID, Multiple: true, ApplyParents: true}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	for name, values := range map[string]map[string]any{
		"Budget": {"period": fiscal.ID, "periods": []any{fiscal.ID}},
		"Report": {"period": q1.ID, "periods": []any{q1.ID}},
		"Loose":  {},
	} {
		if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: name, Values: values}); err != nil {
			t.Fatal(name, err)
		}
	}
	if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "folder", Name: "Archive"}); err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	view := func(name string, settings ViewSettings) *viewstore.ListView {
		t.Helper()
		v, err := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: name, Layout: viewstore.LayoutNavigation, Settings: settings})
		if err != nil {
			t.Fatal(name, err)
		}
		return v
	}
	for _, bad := range []ViewSettings{{}, {Field: "missing"}, {Field: "periods", IncludeSubterms: &no}, {Field: "$keywords", IncludeSubterms: &no}} {
		if _, err = s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Bad", Layout: viewstore.LayoutNavigation, Settings: bad}); err == nil {
			t.Fatal("invalid settings", bad)
		}
	}
	if _, err = s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Bad", Settings: ViewSettings{Field: "period"}}); err == nil {
		t.Fatal("settings on a table view")
	}
	level := func(v *viewstore.ListView, parent string) (string, *int) {
		t.Helper()
		nav, err := s.ViewNavigation(testContext, "alice", v.ID, NavigationRequest{ParentID: parent})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range nav.Data {
			children := ""
			if n.HasChildren {
				children = "+"
			}
			out = append(out, n.Name+children+":"+string(rune('0'+n.Count))+"/"+string(rune('0'+n.SubtreeCount)))
		}
		return strings.Join(out, ","), nav.NoValueCount
	}
	items := func(v *viewstore.ListView, in ViewQueryRequest) string {
		t.Helper()
		page, err := s.QueryView(testContext, "alice", v.ID, in)
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

	// A field without apply_parents lists the items holding the term.
	exact := view("Exact", ViewSettings{Field: "period"})
	if got, none := level(exact, ""); got != "Calendar:0/0,Fiscal+:1/2" || none == nil || *none != 1 {
		t.Fatal("roots", got, none)
	}
	if got, _ := level(exact, fiscal.ID); got != "Q1:1/1,Q2:0/0" {
		t.Fatal("children", got)
	}
	if got := items(exact, ViewQueryRequest{TermID: fiscal.ID}); got != "Budget" {
		t.Fatal("exact items", got)
	}
	// No value lists items only, not folders.
	if got := items(exact, ViewQueryRequest{NoValue: true}); got != "Loose" {
		t.Fatal("no value", got)
	}

	// The view can include subterms; hiding empty terms drops Calendar and Q2.
	rolled := view("Rolled", ViewSettings{Field: "period", IncludeSubterms: &yes, HideEmpty: true})
	if got, _ := level(rolled, ""); got != "Fiscal+:2/2" {
		t.Fatal("rolled roots", got)
	}
	if got := items(rolled, ViewQueryRequest{TermID: fiscal.ID}); got != "Budget,Report" {
		t.Fatal("rolled items", got)
	}

	// A field that applies parents always includes subterms.
	applied := view("Applied", ViewSettings{Field: "periods", HideEmpty: true})
	if got, _ := level(applied, fiscal.ID); got != "Q1:1/1" {
		t.Fatal("applied children", got)
	}
	if got := items(applied, ViewQueryRequest{TermID: fiscal.ID}); got != "Budget,Report" {
		t.Fatal("applied items", got)
	}

	// Item keywords navigate the keywords set and apply their parents.
	finance := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Finance"})
	invoices := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Invoices", ParentID: &finance.ID})
	if _, err = s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: "Invoice", Keywords: []string{invoices.ID}}); err != nil {
		t.Fatal(err)
	}
	keywords := view("Keywords", ViewSettings{Field: "$keywords", HideEmpty: true})
	if got, none := level(keywords, ""); got != "Finance+:1/1" || *none != 3 {
		t.Fatal("keyword roots", got, *none)
	}
	if got := items(keywords, ViewQueryRequest{TermID: finance.ID}); got != "Invoice" {
		t.Fatal("keyword items", got)
	}
	if got := items(keywords, ViewQueryRequest{NoValue: true}); got != "Budget,Loose,Report" {
		t.Fatal("keyword no value", got)
	}

	if _, err = s.QueryView(testContext, "alice", exact.ID, ViewQueryRequest{}); err == nil {
		t.Fatal("a navigation view lists a node")
	}
	if _, err = s.ViewNavigation(testContext, "alice", exact.ID, NavigationRequest{ParentID: finance.ID}); err == nil {
		t.Fatal("parent from another set")
	}
}
