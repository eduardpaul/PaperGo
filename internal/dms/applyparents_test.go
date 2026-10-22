package dms

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	eventstore "papergo/internal/dal/events"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/model"
)

func TestTermFieldsApplyParents(t *testing.T) {
	s, _, l := fixture(t)
	regions, err := s.CreateTermSet(testContext, "alice", TermSetInput{GroupID: testTermGroup(s), Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	europe := mustTerm(t, s, regions.ID, TermInput{Name: "Europe"})
	spain := mustTerm(t, s, regions.ID, TermInput{Name: "Spain", ParentID: &europe.ID})
	madrid := mustTerm(t, s, regions.ID, TermInput{Name: "Madrid", ParentID: &spain.ID})
	america := mustTerm(t, s, regions.ID, TermInput{Name: "America"})
	georgia := mustTerm(t, s, regions.ID, TermInput{Name: "Georgia", ParentID: &america.ID})
	iberia := mustTerm(t, s, regions.ID, TermInput{Name: "Iberia"})

	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "code", Label: "Code", Type: "text", Options: model.FieldOptions{ApplyParents: true}}); err == nil {
		t.Fatal("apply_parents on a text field")
	}
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "loose", Label: "Loose", Type: "term", Options: model.FieldOptions{TermSetID: regions.ID, ApplyParents: true}}); err == nil {
		t.Fatal("apply_parents on an unindexed field")
	}
	for _, f := range []CreateField{
		{Key: "places", Label: "Places", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: regions.ID, Multiple: true, ApplyParents: true}},
		{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: regions.ID}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(f.Key, err)
		}
	}
	item := func(name string, values map[string]any) {
		t.Helper()
		if _, err := s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: name, Values: values}); err != nil {
			t.Fatal(name, err)
		}
	}
	item("A", map[string]any{"places": []any{madrid.ID}, "region": madrid.ID})
	item("B", map[string]any{"places": []any{georgia.ID}, "region": georgia.ID})
	item("C", map[string]any{"places": []any{europe.ID}, "region": europe.ID})
	item("D", map[string]any{"places": []any{madrid.ID, georgia.ID}})
	item("E", map[string]any{"places": []any{iberia.ID}})
	// E keeps the merged term's ID; it now applies Spain's parents.
	if _, err = s.MergeTerm(testContext, "alice", iberia.ID, iberia.Version, MergeTermInput{TargetTermID: spain.ID}); err != nil {
		t.Fatal(err)
	}

	value := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	query := func(field, op string, v any) string {
		t.Helper()
		page, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: field, Op: op, Value: value(v)}, Sort: []SortSpec{{Field: "$name"}}}})
		if err != nil {
			t.Fatal(field, op, err)
		}
		names := []string{}
		for _, r := range page.Data {
			names = append(names, r.Name)
		}
		return strings.Join(names, ",")
	}
	for _, c := range []struct {
		field, op string
		value     any
		want      string
	}{
		{"places", "eq", europe.ID, "A,C,D,E"},
		{"places", "eq", spain.ID, "A,D,E"},
		{"places", "eq", madrid.ID, "A,D"},
		{"places", "in", []string{america.ID}, "B,D"},
		{"places", "in", []string{spain.ID, america.ID}, "A,B,D,E"},
		{"places", "ne", europe.ID, "B"},
		{"places", "under", europe.ID, "A,C,D,E"},
		// Without apply_parents a value matches only itself.
		{"region", "eq", europe.ID, "C"},
		{"region", "ne", europe.ID, "A,B"},
	} {
		if got := query(c.field, c.op, c.value); got != c.want {
			t.Errorf("%s %s %v: %s, want %s", c.field, c.op, c.value, got, c.want)
		}
	}

	defs, err := s.Fields(testContext, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if d.Key != "places" && d.Key != "region" {
			continue
		}
		options := d.Options
		options.ApplyParents = !options.ApplyParents
		if _, err = s.UpdateField(testContext, "alice", l.ID, d.ID, latest(t, s, l.ID).Version, UpdateField{Options: &options}); err == nil || !strings.Contains(err.Error(), "apply_parents are immutable") {
			t.Fatal("apply_parents is immutable", d.Key, err)
		}
	}
}

func TestKeywordsApplyParents(t *testing.T) {
	s, w, l := fixture(t)
	finance := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Finance"})
	invoices := mustTerm(t, s, taxonomy.KeywordsSetID, TermInput{Name: "Invoices", ParentID: &finance.ID})
	if _, err := s.CreateField(testContext, "alice", l.ID, CreateField{Key: "tags", Label: "Tags", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}}); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]CreateResource{
		"Own":   {Kind: "item", Name: "Own", Keywords: []string{invoices.ID}},
		"Field": {Kind: "item", Name: "Field", Values: map[string]any{"tags": []any{invoices.ID}}},
		"Top":   {Kind: "item", Name: "Top", Keywords: []string{finance.ID}},
	} {
		if _, err := s.Create(testContext, "alice", l.ID, in); err != nil {
			t.Fatal(name, err)
		}
	}
	tagged := func(keyword string) string {
		t.Helper()
		raw, _ := json.Marshal(keyword)
		page, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "$keywords", Op: "eq", Value: raw}, Sort: []SortSpec{{Field: "$name"}}}})
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, r := range page.Data {
			names = append(names, r.Name)
		}
		browsed, err := s.Browse(testContext, "alice", Browse{ParentID: l.ID, Keyword: keyword})
		if err != nil {
			t.Fatal(err)
		}
		other := []string{}
		for _, r := range browsed.Data {
			other = append(other, r.Name)
		}
		slices.Sort(other)
		if !slices.Equal(names, other) {
			t.Fatalf("%s: query %v, browse %v", keyword, names, other)
		}
		return strings.Join(names, ",")
	}
	vocabulary := func() string {
		t.Helper()
		page, err := s.WorkspaceKeywords(testContext, "alice", w.ID, WorkspaceKeywordsQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range page.Data {
			out = append(out, c.Name+":"+string(rune('0'+c.Count)))
		}
		return strings.Join(out, ",")
	}
	// An item's own keywords apply their parents; a keywords field without
	// apply_parents does not.
	if got := tagged(finance.ID); got != "Own,Top" {
		t.Fatal("parent keyword", got)
	}
	if got := tagged(invoices.ID); got != "Field,Own" {
		t.Fatal("child keyword", got)
	}
	if got := vocabulary(); got != "Finance:2,Invoices:2" {
		t.Fatal("vocabulary", got)
	}

	if _, err := s.MoveTerm(testContext, "alice", invoices.ID, invoices.Version, MoveTermInput{}); err != nil {
		t.Fatal(err)
	}
	if got := tagged(finance.ID); got != "Top" {
		t.Fatal("a move changes the parents applied", got)
	}
	if got := vocabulary(); got != "Finance:1,Invoices:2" {
		t.Fatal("vocabulary after the move", got)
	}
	events, err := s.DAL.Events().List(testContext, eventstore.Filter{Types: []string{EventTermMoved}})
	if err != nil || len(events) != 1 || events[0].Data["term_id"] != invoices.ID || events[0].Data["previous_parent_id"] != finance.ID || events[0].Data["parent_id"] != "" {
		t.Fatal("term.moved", events, err)
	}
}
