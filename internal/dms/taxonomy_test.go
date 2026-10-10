package dms

import (
	"encoding/json"
	"errors"
	"papergo/ent"
	"papergo/ent/domainevent"
	"papergo/internal/model"
	"slices"
	"strings"
	"testing"
)

func mustTerm(t *testing.T, s *Service, setID string, in TermInput) TermView {
	t.Helper()
	v, err := s.CreateTerm(testContext, "alice", setID, in)
	if err != nil {
		t.Fatal(in.Name, err)
	}
	return v
}

func TestWorkspaceTaxonomyGroupsAndSets(t *testing.T) {
	s, w, _ := fixture(t)
	groups, err := s.TermGroups(testContext, "alice", w.ID, "", 10)
	if err != nil || len(groups.Data) != 1 || !groups.Data[0].IsSystem || groups.Data[0].Name != SystemTermGroupName {
		t.Fatal("system group", groups, err)
	}
	system := groups.Data[0]
	sets, err := s.TermSets(testContext, "alice", w.ID, system.ID, "", 10)
	if err != nil || len(sets.Data) != 1 || !sets.Data[0].IsKeywords || !sets.Data[0].IsOpen || sets.Data[0].Key != KeywordsTermSetKey {
		t.Fatal("keywords set", sets, err)
	}
	keywords := sets.Data[0]
	if _, err = s.UpdateTermGroup(testContext, "alice", system.ID, system.Version, TermGroupInput{Name: "Mine"}); err == nil {
		t.Fatal("renamed the system group")
	}
	if err = s.DeleteTermGroup(testContext, "alice", system.ID, system.Version); err == nil {
		t.Fatal("deleted the system group")
	}
	if _, err = s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: system.ID, Key: "extra", Name: "Extra"}); err == nil {
		t.Fatal("set in the system group")
	}
	if _, err = s.UpdateTermSet(testContext, "alice", keywords.ID, keywords.Version, TermSetInput{Name: "Tags"}); err == nil {
		t.Fatal("closed the keywords set")
	}
	if err = s.DeleteTermSet(testContext, "alice", keywords.ID, keywords.Version); err == nil {
		t.Fatal("deleted the keywords set")
	}

	group, err := s.CreateTermGroup(testContext, "alice", w.ID, TermGroupInput{Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTermGroup(testContext, "alice", w.ID, TermGroupInput{Name: "Records"}); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate group name", err)
	}
	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions"})
	if err != nil || set.IsOpen {
		t.Fatal("sets are closed unless asked", set, err)
	}
	if err = s.DeleteTermGroup(testContext, "alice", group.ID, group.Version); err == nil {
		t.Fatal("deleted a group with sets")
	}
	if err = s.DeleteTermSet(testContext, "alice", set.ID, set.Version); err != nil {
		t.Fatal("empty set", err)
	}
	if err = s.DeleteTermGroup(testContext, "alice", group.ID, group.Version); err != nil {
		t.Fatal("empty group", err)
	}

	used, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: testTermGroup(s, w.ID), Key: "used", Name: "Used"})
	if err != nil {
		t.Fatal(err)
	}
	list := create(t, s, w.ID, "list", "Tagged", nil)
	if _, err = s.CreateField(testContext, "alice", list.ID, CreateField{Key: "topic", Label: "Topic", Type: "term", Options: model.FieldOptions{TermSetID: used.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteTermSet(testContext, "alice", used.ID, used.Version); err == nil {
		t.Fatal("deleted a set used by a field")
	}
	mustTerm(t, s, used.ID, TermInput{Name: "Kept"})
	if _, err = s.Client.ExecContext(testContext, "DELETE FROM terms"); err == nil {
		t.Fatal("terms are retained")
	}
}

func TestTermTreeOpenSetsAndListing(t *testing.T) {
	s, w, _ := fixture(t)
	if _, err := s.SetPermissions(testContext, "alice", w.ID, latest(t, s, w.ID).Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"writer", "write", "allow"}, {"reader", "read", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	group := testTermGroup(s, w.ID)
	closed, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group, Key: "places", Name: "Places"})
	if err != nil {
		t.Fatal(err)
	}
	open, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTerm(testContext, "writer", closed.ID, TermInput{Name: "Spain"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("writer added to a closed set", err)
	}
	if _, err = s.CreateTerm(testContext, "writer", open.ID, TermInput{Name: "Travel"}); err != nil {
		t.Fatal("writer adds to an open set", err)
	}
	if _, err = s.CreateTerm(testContext, "writer", open.ID, TermInput{Name: "Retired", Deprecated: true}); !errors.Is(err, ErrForbidden) {
		t.Fatal("writer added a deprecated term", err)
	}

	europe := mustTerm(t, s, closed.ID, TermInput{Name: "Europe", SortOrder: 2})
	america := mustTerm(t, s, closed.ID, TermInput{Name: "America", SortOrder: 1})
	color := "#00aa11"
	spain := mustTerm(t, s, closed.ID, TermInput{Name: "Spain", ParentID: &europe.ID, Description: "Kingdom", Color: &color})
	mustTerm(t, s, closed.ID, TermInput{Name: "Georgia", ParentID: &europe.ID})
	mustTerm(t, s, closed.ID, TermInput{Name: "Georgia", ParentID: &america.ID})
	if _, err = s.CreateTerm(testContext, "alice", closed.ID, TermInput{Name: "georgia", ParentID: &europe.ID}); !errors.Is(err, ErrConflict) {
		t.Fatal("sibling names are unique", err)
	}
	bad := "#00AA11"
	if _, err = s.CreateTerm(testContext, "alice", closed.ID, TermInput{Name: "Bad", Color: &bad}); err == nil {
		t.Fatal("uppercase color")
	}
	if spain.Path != europe.Path+spain.ID+"/" || spain.Description != "Kingdom" || *spain.Color != color {
		t.Fatal("term details", spain.Term)
	}

	roots, err := s.Terms(testContext, "reader", closed.ID, TermsQuery{})
	if err != nil || len(roots.Data) != 2 || roots.Data[0].ID != america.ID || !roots.Data[1].HasChildren {
		t.Fatal("roots by sort order", roots, err)
	}
	first, err := s.Terms(testContext, "reader", closed.ID, TermsQuery{ParentID: europe.ID, Limit: 1})
	if err != nil || len(first.Data) != 1 || first.Data[0].Name != "Georgia" || first.NextCursor == "" {
		t.Fatal("children page", first, err)
	}
	next, err := s.Terms(testContext, "reader", closed.ID, TermsQuery{ParentID: europe.ID, Limit: 1, After: first.NextCursor})
	if err != nil || len(next.Data) != 1 || next.Data[0].ID != spain.ID || next.NextCursor != "" {
		t.Fatal("next page", next, err)
	}
	found, err := s.Terms(testContext, "reader", closed.ID, TermsQuery{Search: "georgia", ParentID: america.ID})
	if err != nil || len(found.Data) != 1 || found.Data[0].ParentID == nil || *found.Data[0].ParentID != america.ID {
		t.Fatal("search below a term", found, err)
	}
	if _, err = s.UpdateTerm(testContext, "alice", spain.ID, spain.Version, TermInput{Name: "Spain", Deprecated: true}); err != nil {
		t.Fatal(err)
	}
	if page, _ := s.Terms(testContext, "reader", closed.ID, TermsQuery{ParentID: europe.ID}); len(page.Data) != 1 {
		t.Fatal("deprecated terms are hidden", page)
	}
	if page, _ := s.Terms(testContext, "reader", closed.ID, TermsQuery{ParentID: europe.ID, IncludeDeprecated: true}); len(page.Data) != 2 || page.Data[1].Color != nil {
		t.Fatal("deprecated terms on request, color cleared", page)
	}

	byID, err := s.TermsByID(testContext, "reader", []string{spain.ID, "missing", america.ID})
	if err != nil || len(byID) != 2 || byID[0].ID != spain.ID || byID[1].ID != america.ID {
		t.Fatal("batch lookup", byID, err)
	}
	if byID, err = s.TermsByID(testContext, "stranger", []string{spain.ID}); err != nil || len(byID) != 0 {
		t.Fatal("batch lookup hides unreadable terms", byID, err)
	}
}

func TestMoveAndMergeTerms(t *testing.T) {
	s, w, l := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: testTermGroup(s, w.ID), Key: "topics", Name: "Topics"})
	if err != nil {
		t.Fatal(err)
	}
	finance := mustTerm(t, s, set.ID, TermInput{Name: "Finance", Synonyms: []string{"Money"}})
	money := mustTerm(t, s, set.ID, TermInput{Name: "Accounting", Synonyms: []string{"Books"}})
	tax := mustTerm(t, s, set.ID, TermInput{Name: "Tax", ParentID: &money.ID})
	vat := mustTerm(t, s, set.ID, TermInput{Name: "VAT", ParentID: &tax.ID})

	if _, err = s.MoveTerm(testContext, "alice", money.ID, money.Version, MoveTermInput{ParentID: &vat.ID}); err == nil {
		t.Fatal("moved below itself")
	}
	moved, err := s.MoveTerm(testContext, "alice", tax.ID, tax.Version, MoveTermInput{ParentID: &finance.ID})
	if err != nil || moved.Path != finance.Path+tax.ID+"/" || *moved.ParentID != finance.ID {
		t.Fatal("move", moved.Term, err)
	}
	child, _ := s.Term(testContext, "alice", vat.ID)
	if child.Path != moved.Path+vat.ID+"/" {
		t.Fatal("descendant path", child.Path)
	}
	if _, err = s.MoveTerm(testContext, "alice", tax.ID, tax.Version, MoveTermInput{}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale move", err)
	}
	back, err := s.MoveTerm(testContext, "alice", tax.ID, moved.Version, MoveTermInput{ParentID: &money.ID})
	if err != nil {
		t.Fatal(err)
	}

	field := CreateField{Key: "topic", Label: "Topic", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}}
	if _, err = s.CreateField(testContext, "alice", l.ID, field); err != nil {
		t.Fatal(err)
	}
	create(t, s, l.ID, "item", "Ledger", map[string]any{"topic": money.ID})
	old := mustTerm(t, s, set.ID, TermInput{Name: "Bookkeeping"})
	current, _ := s.Term(testContext, "alice", old.ID)
	if _, err = s.MergeTerm(testContext, "alice", old.ID, current.Version, MergeTermInput{TargetTermID: money.ID}); err != nil {
		t.Fatal(err)
	}
	money, _ = s.Term(testContext, "alice", money.ID)
	target, err := s.MergeTerm(testContext, "alice", money.ID, money.Version, MergeTermInput{TargetTermID: finance.ID})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(target.Synonyms, ",") != "Money,Accounting,Books,Bookkeeping" || !target.HasChildren {
		t.Fatal("merged synonyms and children", target.Term)
	}
	source, _ := s.Term(testContext, "alice", money.ID)
	if source.MergedIntoID == nil || *source.MergedIntoID != finance.ID || !source.Deprecated {
		t.Fatal("source", source.Term)
	}
	chained, _ := s.Term(testContext, "alice", old.ID)
	if *chained.MergedIntoID != finance.ID {
		t.Fatal("earlier merges point at the target", chained.Term)
	}
	child, _ = s.Term(testContext, "alice", tax.ID)
	if *child.ParentID != finance.ID || child.Version != back.Version+1 {
		t.Fatal("children move to the target", child.Term)
	}
	if child, _ = s.Term(testContext, "alice", vat.ID); child.Path != finance.Path+tax.ID+"/"+vat.ID+"/" {
		t.Fatal("grandchild path", child.Path)
	}
	if _, err = s.UpdateTerm(testContext, "alice", money.ID, source.Version, TermInput{Name: "Again"}); err == nil {
		t.Fatal("merged terms are read-only")
	}
	if _, err = s.MergeTerm(testContext, "alice", finance.ID, target.Version, MergeTermInput{TargetTermID: tax.ID}); err == nil {
		t.Fatal("merged into a descendant")
	}
	events, err := s.Client.DomainEvent.Query().Where(domainevent.TypeEQ(EventTermMerged)).All(testContext)
	if err != nil || len(events) != 2 || events[1].Data["target_term_id"] != finance.ID {
		t.Fatal("term.merged events", events, err)
	}
	if page, _ := s.Terms(testContext, "alice", set.ID, TermsQuery{IncludeDeprecated: true}); len(page.Data) != 1 {
		t.Fatal("merged terms are not listed", page)
	}
}

func TestKeywords(t *testing.T) {
	s, w, l := fixture(t)
	if _, err := s.SetPermissions(testContext, "alice", w.ID, latest(t, s, w.ID).Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"writer", "write", "allow"}, {"reader", "read", "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddKeyword(testContext, "reader", w.ID, KeywordInput{Name: "budget"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("reader added a keyword", err)
	}
	budget, created, err := s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: " Budget "})
	if err != nil || !created || budget.Name != "Budget" {
		t.Fatal("keyword", budget, created, err)
	}
	again, created, err := s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: "BUDGET"})
	if err != nil || created || again.ID != budget.ID {
		t.Fatal("get or create", again, created, err)
	}
	trip, _, _ := s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: "Trip"})
	mustTerm(t, s, budget.TermSetID, TermInput{Name: "Business trip"})
	suggested, err := s.Keywords(testContext, "reader", w.ID, "trip", 0)
	if err != nil || len(suggested) != 2 || suggested[0].ID != trip.ID {
		t.Fatal("prefix matches first", suggested, err)
	}

	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: testTermGroup(s, w.ID), Key: "costs", Name: "Costs"})
	if err != nil {
		t.Fatal(err)
	}
	travel := mustTerm(t, s, set.ID, TermInput{Name: "Travel", Labels: map[string]string{"en": "Trip"}})
	if _, err = s.PromoteKeyword(testContext, "writer", trip.ID, trip.Version, PromoteKeywordInput{TermSetID: set.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("writer promoted", err)
	}
	merged, err := s.PromoteKeyword(testContext, "alice", trip.ID, trip.Version, PromoteKeywordInput{TermSetID: set.ID})
	if err != nil || merged.ID != travel.ID || !merged.AvailableAsKeyword {
		t.Fatal("promote merges into a matching label", merged.Term, err)
	}
	promoted, err := s.PromoteKeyword(testContext, "alice", budget.ID, budget.Version, PromoteKeywordInput{TermSetID: set.ID, ParentID: &travel.ID})
	if err != nil || promoted.ID != budget.ID || promoted.TermSetID != set.ID || !promoted.AvailableAsKeyword || promoted.Path != travel.Path+budget.ID+"/" {
		t.Fatal("promote moves", promoted.Term, err)
	}
	suggested, _ = s.Keywords(testContext, "reader", w.ID, "", 0)
	names := []string{}
	for _, v := range suggested {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "Budget,Business trip,Travel" {
		t.Fatal("keywords include promoted terms", names)
	}
	if again, created, _ = s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: "trip"}); created || again.ID != travel.ID {
		t.Fatal("a merged keyword name finds its term", again.Term, created)
	}

	keywordsSet, _ := s.keywordsSet(testContext, w.ID)
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "keywords", Label: "Keywords", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: keywordsSet.ID, Multiple: true}}); err != nil {
		t.Fatal(err)
	}
	lunch, _, _ := s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: "Lunch"})
	snack, _, _ := s.AddKeyword(testContext, "writer", w.ID, KeywordInput{Name: "Snack"})
	create(t, s, l.ID, "item", "One", map[string]any{"keywords": []any{lunch.ID, snack.ID}})
	create(t, s, l.ID, "item", "Two", map[string]any{"keywords": []any{lunch.ID}})
	if _, err = s.PopularKeywords(testContext, "writer", w.ID, 10); !errors.Is(err, ErrForbidden) {
		t.Fatal("popular keywords are for managers", err)
	}
	popular, err := s.PopularKeywords(testContext, "alice", w.ID, 10)
	if err != nil || len(popular) != 2 || popular[0].Term.ID != lunch.ID || popular[0].Count != 2 || popular[1].Count != 1 {
		t.Fatal("popular", popular, err)
	}
}

func TestImportTermsCSV(t *testing.T) {
	s, w, _ := fixture(t)
	group, err := s.CreateTermGroup(testContext, "alice", w.ID, TermGroupInput{Name: "Imported"})
	if err != nil {
		t.Fatal(err)
	}
	csv := "\ufeff\"Term Set Name\",\"Term Set Description\",\"LCID\",\"Available for Tagging\",\"Term Description\",\"Level 1 Term\",\"Level 2 Term\",\"Level 3 Term\",\"Level 4 Term\",\"Level 5 Term\",\"Level 6 Term\",\"Level 7 Term\"\n" +
		"\"Departments\",\"Company departments\",,TRUE,,,,,,,,\n" +
		",,1033,TRUE,\"Money matters\",\"Finance\",,,,,,\n" +
		",,1033,TRUE,,\"Finance\",\"Payroll\",,,,,\n" +
		",,1033,FALSE,\"Old\",\"Finance\",\"Legacy\",,,,,\n" +
		"\"2024 Regions\",,,TRUE,,\"Europe\",\"Spain\",,,,,\n"
	out, err := s.ImportTerms(testContext, "alice", group.ID, strings.NewReader(csv))
	if err != nil || out.SetsCreated != 2 || out.TermsCreated != 5 || len(out.TermSets) != 2 {
		t.Fatal("import", out, err)
	}
	if out.TermSets[0].Key != "departments" || out.TermSets[0].Description != "Company departments" || out.TermSets[0].IsOpen || out.TermSets[1].Key != "set_2024_regions" {
		t.Fatal("imported sets", out.TermSets[0], out.TermSets[1])
	}
	roots, _ := s.Terms(testContext, "alice", out.TermSets[0].ID, TermsQuery{})
	if len(roots.Data) != 1 || roots.Data[0].Description != "Money matters" {
		t.Fatal("roots", roots)
	}
	children, _ := s.Terms(testContext, "alice", out.TermSets[0].ID, TermsQuery{ParentID: roots.Data[0].ID, IncludeDeprecated: true})
	if len(children.Data) != 2 || children.Data[0].Name != "Legacy" || !children.Data[0].Deprecated || children.Data[1].Deprecated {
		t.Fatal("children", children)
	}
	again, err := s.ImportTerms(testContext, "alice", group.ID, strings.NewReader(csv))
	if err != nil || again.SetsCreated != 0 || again.TermsCreated != 0 {
		t.Fatal("import is additive", again, err)
	}
	if _, err = s.ImportTerms(testContext, "alice", group.ID, strings.NewReader("\"Level 1 Term\"\nX\n")); err == nil {
		t.Fatal("missing term set column")
	}
	if _, err = s.ImportTerms(testContext, "alice", group.ID, strings.NewReader("Term Set Name,Level 1 Term,Level 2 Term\nA,,B\n")); err == nil {
		t.Fatal("level gap")
	}
	groups, _ := s.TermGroups(testContext, "alice", w.ID, "", 10)
	for _, g := range groups.Data {
		if g.IsSystem {
			if _, err = s.ImportTerms(testContext, "alice", g.ID, strings.NewReader(csv)); err == nil {
				t.Fatal("import into the system group")
			}
		}
	}
}

func TestTermAndKeywordsFieldValues(t *testing.T) {
	s, w, l := fixture(t)
	group := testTermGroup(s, w.ID)
	regions, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group, Key: "regions", Name: "Regions"})
	if err != nil {
		t.Fatal(err)
	}
	topics, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	europe := mustTerm(t, s, regions.ID, TermInput{Name: "Europe"})
	america := mustTerm(t, s, regions.ID, TermInput{Name: "America"})
	spain := mustTerm(t, s, regions.ID, TermInput{Name: "Spain", ParentID: &europe.ID, Labels: map[string]string{"es": "España"}, Synonyms: []string{"Iberia"}})
	mustTerm(t, s, regions.ID, TermInput{Name: "Georgia", ParentID: &europe.ID})
	mustTerm(t, s, regions.ID, TermInput{Name: "Georgia", ParentID: &america.ID})
	if _, err = s.CreateField(testContext, "alice", l.ID, CreateField{Key: "tagged", Label: "Tagged", Type: "keywords", Options: model.FieldOptions{Multiple: true}}); err == nil {
		t.Fatal("unindexed keywords field")
	}
	for _, f := range []CreateField{
		{Key: "region", Label: "Region", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: regions.ID}},
		{Key: "topics", Label: "Topics", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: topics.ID, Multiple: true}},
		{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(f.Key, err)
		}
	}
	item := func(name string, values map[string]any) (*ent.Resource, error) {
		return s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: name, Values: values})
	}
	for label, want := range map[string]string{"georgia": "matches several", "Atlantis": "closed term set"} {
		if _, err = item("Bad", map[string]any{"region": label}); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", label, err)
		}
	}
	if _, err = item("Bad", map[string]any{"region": america.ID + "x"}); err == nil {
		t.Fatal("unknown label")
	}
	madrid, err := item("Madrid", map[string]any{"region": "españa", "topics": []any{"Finance", " finance "}, "keywords": []any{"Lunch", "lunch"}})
	if err != nil {
		t.Fatal(err)
	}
	if madrid.Values["region"] != spain.ID || len(madrid.Values["topics"].([]any)) != 1 || len(madrid.Values["keywords"].([]any)) != 1 {
		t.Fatal("labels resolve to term IDs", madrid.Values)
	}
	finance, _ := s.Term(testContext, "alice", madrid.Values["topics"].([]any)[0].(string))
	lunch, _ := s.Term(testContext, "alice", madrid.Values["keywords"].([]any)[0].(string))
	if finance.Name != "Finance" || finance.TermSetID != topics.ID || lunch.Name != "Lunch" || lunch.ParentID != nil {
		t.Fatal("open sets get new terms", finance.Term, lunch.Term)
	}
	if _, err = item("Wrong set", map[string]any{"keywords": []any{spain.ID}}); err == nil {
		t.Fatal("a managed term is not a keyword")
	}
	if _, err = s.UpdateTerm(testContext, "alice", spain.ID, spain.Version, TermInput{Name: "Spain", Labels: spain.Labels, Synonyms: spain.Synonyms, AvailableAsKeyword: true}); err != nil {
		t.Fatal(err)
	}
	paris, err := item("Paris", map[string]any{"region": "Europe", "topics": []any{"Money"}, "keywords": []any{spain.ID, "iberia"}})
	if err != nil || len(paris.Values["keywords"].([]any)) != 1 {
		t.Fatal("promoted terms are keywords, found by synonym", paris.Values, err)
	}

	money, _ := s.Term(testContext, "alice", paris.Values["topics"].([]any)[0].(string))
	if _, err = s.MergeTerm(testContext, "alice", money.ID, money.Version, MergeTermInput{TargetTermID: finance.ID}); err != nil {
		t.Fatal(err)
	}
	query := func(f FilterExpr) []string {
		t.Helper()
		raw, _ := json.Marshal(f)
		var spec FilterExpr
		_ = json.Unmarshal(raw, &spec)
		page, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &spec, Sort: SortSpec{Field: "$name"}}})
		if err != nil {
			t.Fatal(f, err)
		}
		names := []string{}
		for _, r := range page.Data {
			names = append(names, r.Name)
		}
		return names
	}
	value := func(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	if got := query(FilterExpr{Field: "topics", Op: "eq", Value: value(finance.ID)}); strings.Join(got, ",") != "Madrid,Paris" {
		t.Fatal("eq matches terms merged into the value", got)
	}
	if got := query(FilterExpr{Field: "region", Op: "under", Value: value(europe.ID)}); strings.Join(got, ",") != "Madrid,Paris" {
		t.Fatal("under matches the term and its descendants", got)
	}
	if got := query(FilterExpr{Field: "region", Op: "under", Value: value(spain.ID)}); strings.Join(got, ",") != "Madrid" {
		t.Fatal("under a leaf", got)
	}
	if got := query(FilterExpr{Field: "region", Op: "ne", Value: value(europe.ID)}); strings.Join(got, ",") != "Madrid" {
		t.Fatal("ne", got)
	}
	if got := query(FilterExpr{Field: "topics", Op: "in", Value: value([]string{money.ID})}); strings.Join(got, ",") != "Paris" {
		t.Fatal("in on a merged term", got)
	}
	if _, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Filter: &FilterExpr{Field: "$name", Op: "under", Value: value("x")}}}); err == nil {
		t.Fatal("under on a text field")
	}

	name := "Paris, France"
	updated, err := s.Update(testContext, "alice", paris.ID, latest(t, s, paris.ID).Version, UpdateResource{Name: &name})
	if err != nil || updated.Values["topics"].([]any)[0] != finance.ID {
		t.Fatal("a write replaces merged terms", updated.Values, err)
	}
	groups, err := s.QueryGroups(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{GroupBy: "region"}})
	if err != nil || len(groups.Data) != 2 {
		t.Fatal("groups", groups, err)
	}
	for _, g := range groups.Data {
		if g.Value == spain.ID && g.Label != "Spain" || g.Value == europe.ID && g.Label != "Europe" {
			t.Fatal("group labels", groups.Data)
		}
	}
	found, err := s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Search: "iberia"}})
	if err != nil || len(found.Data) != 2 {
		t.Fatal("search finds term synonyms", found, err)
	}
	found, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Search: "españa"}})
	if err != nil || len(found.Data) != 2 {
		t.Fatal("search finds localized labels", found, err)
	}
	found, err = s.Query(testContext, "alice", l.ID, QueryRequest{Query: QuerySpec{Search: "lunch"}})
	if err != nil || len(found.Data) != 1 || found.Data[0].ID != madrid.ID {
		t.Fatal("search finds keywords", found, err)
	}
	popular, err := s.PopularKeywords(testContext, "alice", w.ID, 10)
	if err != nil || len(popular) != 2 {
		t.Fatal("popular counts keywords fields", popular, err)
	}
}

func TestSmartFoldersMatchKeywordsAndMergedTerms(t *testing.T) {
	s, w, l := fixture(t)
	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: testTermGroup(s, w.ID), Key: "projects", Name: "Projects"})
	if err != nil {
		t.Fatal(err)
	}
	apollo := mustTerm(t, s, set.ID, TermInput{Name: "Apollo"})
	lander := mustTerm(t, s, set.ID, TermInput{Name: "Lander", ParentID: &apollo.ID})
	old := mustTerm(t, s, set.ID, TermInput{Name: "Moonshot"})
	for _, f := range []CreateField{
		{Key: "project", Label: "Project", Type: "term", Indexed: true, Options: model.FieldOptions{TermSetID: set.ID}},
		{Key: "keywords", Label: "Keywords", Type: "keywords", Indexed: true, Options: model.FieldOptions{Multiple: true}},
	} {
		if _, err = s.CreateField(testContext, "alice", l.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	create(t, s, l.ID, "item", "Spec", map[string]any{"project": lander.ID, "keywords": []any{"Budget"}})
	create(t, s, l.ID, "item", "Pitch", map[string]any{"project": old.ID})
	create(t, s, l.ID, "item", "Lunch", map[string]any{"keywords": []any{"Food"}})
	if _, err = s.MergeTerm(testContext, "alice", old.ID, old.Version, MergeTermInput{TargetTermID: apollo.ID}); err != nil {
		t.Fatal(err)
	}
	names := func(terms ...string) string {
		t.Helper()
		f := smartOK(t, s, SmartFolderInput{Name: strings.Join(terms, " "), WorkspaceID: &w.ID, Definition: SmartFolderDefinition{Terms: terms}})
		got, err := s.QuerySmartFolder(testContext, "alice", f.ID, SmartFolderQueryRequest{})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, e := range got.Data {
			out = append(out, e.Item.Name)
		}
		slices.Sort(out)
		return strings.Join(out, ",")
	}
	if got := names(apollo.ID); got != "Pitch,Spec" {
		t.Fatal("subtree and merged terms", got)
	}
	if got := names(old.ID); got != "Pitch,Spec" {
		t.Fatal("a merged term stands for its target", got)
	}
	budget, _, _ := s.AddKeyword(testContext, "alice", w.ID, KeywordInput{Name: "budget"})
	if got := names(budget.ID); got != "Spec" {
		t.Fatal("keywords fields", got)
	}
}

func TestTaxonomyPackageRoundTrip(t *testing.T) {
	s, w, _ := fixture(t)
	group, err := s.CreateTermGroup(testContext, "alice", w.ID, TermGroupInput{Name: "Records", Description: "Filing"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(testContext, "alice", w.ID, TermSetInput{GroupID: group.ID, Key: "regions", Name: "Regions", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	color := "#112233"
	europe := mustTerm(t, s, set.ID, TermInput{Name: "Europe", Color: &color, SortOrder: 3, Labels: map[string]string{"es": "Europa"}})
	mustTerm(t, s, set.ID, TermInput{Name: "Spain", ParentID: &europe.ID, Synonyms: []string{"Iberia"}, AvailableAsKeyword: true})
	old := mustTerm(t, s, set.ID, TermInput{Name: "Old Europe"})
	if _, err = s.MergeTerm(testContext, "alice", old.ID, old.Version, MergeTermInput{TargetTermID: europe.ID}); err != nil {
		t.Fatal(err)
	}
	s.AddKeyword(testContext, "alice", w.ID, KeywordInput{Name: "Budget"})
	pkg, err := s.ExportTaxonomy(testContext, "alice", w.ID)
	if err != nil || len(pkg.Groups) != 2 || !pkg.Groups[0].System || pkg.Groups[1].Sets[0].Terms[0].Children[0].Name != "Spain" || len(pkg.Groups[1].Sets[0].Terms) != 1 {
		t.Fatal("export", pkg, err)
	}
	raw, _ := json.Marshal(pkg)
	if strings.Contains(string(raw), europe.ID) || strings.Contains(string(raw), `"name":"Old Europe"`) {
		t.Fatal("packages hold names, not IDs or merged terms", string(raw))
	}

	target := create(t, s, "", "workspace", "Copy", nil)
	out, err := s.ImportTaxonomy(testContext, "alice", target.ID, pkg)
	if err != nil || out.GroupsCreated != 1 || out.SetsCreated != 1 || out.TermsCreated != 3 {
		t.Fatal("import", out, err)
	}
	again, err := s.ImportTaxonomy(testContext, "alice", target.ID, pkg)
	if err != nil || again != (TaxonomyImportResult{}) {
		t.Fatal("import is additive", again, err)
	}
	copied, err := s.ExportTaxonomy(testContext, "alice", target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(copied); string(got) != string(raw) {
		t.Fatalf("round trip\n%s\n%s", raw, got)
	}
	pkg.Groups[1].Sets = append(pkg.Groups[1].Sets, PortableTermSet{Key: "regions", Name: "Again"})
	if _, err = s.ImportTaxonomy(testContext, "alice", target.ID, pkg); err == nil {
		t.Fatal("duplicate set keys")
	}
}
