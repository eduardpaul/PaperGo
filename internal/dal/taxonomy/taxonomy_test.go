package taxonomy_test

import (
	"context"
	"errors"
	"papergo/internal/dal/daltest"
	"slices"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
	"papergo/internal/model"
)

func TestTaxonomy_SQLite(t *testing.T)   { testTaxonomy(t, daltest.SQLiteStore(t)) }
func TestTaxonomy_Postgres(t *testing.T) { testTaxonomy(t, daltest.PostgresStore(t)) }

func testTaxonomy(t *testing.T, s *dal.Store) {
	ctx := context.Background()
	r := s.Taxonomy()
	if _, err := r.CreateSet(ctx, taxonomy.Set{GroupID: taxonomy.SystemGroupID, Key: "extra", Name: "Extra"}); !taxonomy.IsViolation(err) {
		t.Fatalf("set in the system group: %v", err)
	}
	places := daltest.TermSet(t, s, "places")
	other := daltest.TermSet(t, s, "other")
	add := func(set, parent, id, name string, labels map[string]string, synonyms ...string) taxonomy.Term {
		t.Helper()
		v, err := r.AddTerm(ctx, taxonomy.Term{ID: id, SetID: set, ParentID: parent, Name: name, Labels: labels, Synonyms: synonyms})
		if err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
		return v
	}
	europe := add(places.ID, "", "europe", "Europe", map[string]string{"de": "Europa"})
	spain := add(places.ID, europe.ID, "spain", "Spain", map[string]string{"es": "España"}, "Kingdom of Spain")
	madrid := add(places.ID, spain.ID, "madrid", "Madrid", nil)
	asia := add(places.ID, "", "asia", "Asia", nil)
	if madrid.Path != "/europe/spain/madrid/" || madrid.Levels() != 3 {
		t.Fatalf("path: %+v", madrid)
	}

	ids := func(terms []taxonomy.Term, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, len(terms))
		for i, v := range terms {
			out[i] = v.ID
		}
		return out
	}
	for name, c := range map[string]struct {
		f    taxonomy.TermFilter
		want []string
	}{
		"roots":           {taxonomy.TermFilter{SetID: places.ID, Roots: true}, []string{"asia", "europe"}},
		"children":        {taxonomy.TermFilter{ParentID: europe.ID}, []string{"spain"}},
		"under":           {taxonomy.TermFilter{Under: europe.Path}, []string{"madrid", "spain"}},
		"search label":    {taxonomy.TermFilter{SetID: places.ID, Search: "españa"}, []string{"spain"}},
		"search synonym":  {taxonomy.TermFilter{SetID: places.ID, Search: "kingdom"}, []string{"spain"}},
		"search name":     {taxonomy.TermFilter{SetID: places.ID, Search: "a"}, []string{"asia", "europe", "madrid", "spain"}},
		"match label":     {taxonomy.TermFilter{Match: "europa", In: taxonomy.MatchLabels}, []string{"europe"}},
		"match not names": {taxonomy.TermFilter{Match: "europa", In: taxonomy.MatchName | taxonomy.MatchSynonyms}, []string{}},
		"after":           {taxonomy.TermFilter{SetID: places.ID, After: &taxonomy.Cursor{Name: "europe", ID: "europe"}}, []string{"madrid", "spain"}},
		"scope":           {taxonomy.TermFilter{Scope: &taxonomy.Scope{Path: spain.Path, IDs: []string{"europe"}}}, []string{"europe", "madrid", "spain"}},
		"leaf scope":      {taxonomy.TermFilter{Scope: &taxonomy.Scope{ParentIDs: []string{"europe"}}}, []string{"spain"}},
		"prefix first":    {taxonomy.TermFilter{SetID: places.ID, Search: "s", Order: taxonomy.OrderPrefix}, []string{"spain", "asia"}},
	} {
		if got := ids(r.Terms(ctx, c.f)); !slices.Equal(got, c.want) {
			if c.f.Order == taxonomy.OrderTree {
				slices.Sort(got)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
	}
	parents, err := r.Parents(ctx, []string{"europe", "spain", "madrid"})
	if err != nil || !parents["europe"] || !parents["spain"] || parents["madrid"] {
		t.Fatalf("parents: %v %v", parents, err)
	}

	// A subtree moves to another set below one of its terms.
	if _, err = r.Move(ctx, europe.ID, europe.Version, places.ID, madrid.ID, false); !taxonomy.IsViolation(err) {
		t.Fatalf("moved below itself: %v", err)
	}
	world := add(other.ID, "", "world", "World", nil)
	moved, err := r.Move(ctx, spain.ID, spain.Version, other.ID, world.ID, false)
	if err != nil || moved.Path != "/world/spain/" || moved.SetID != other.ID {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if madrid, err = r.Term(ctx, madrid.ID); err != nil || madrid.Path != "/world/spain/madrid/" || madrid.SetID != other.ID {
		t.Fatalf("moved descendant: %+v %v", madrid, err)
	}
	if _, err = r.Move(ctx, spain.ID, spain.Version, other.ID, "", false); !errors.Is(err, dberr.ErrConcurrentUpdate) {
		t.Fatalf("stale move: %v", err)
	}

	// Merging moves children and points earlier merges at the target.
	catalonia := add(other.ID, "", "catalonia", "Catalonia", nil, "Catalunya")
	barcelona := add(other.ID, catalonia.ID, "barcelona", "Barcelona", nil)
	if catalonia, err = r.Merge(ctx, asia.ID, asia.Version, catalonia.ID, false); err != nil {
		t.Fatal(err)
	}
	target, err := r.Merge(ctx, catalonia.ID, catalonia.Version, moved.ID, false)
	if err != nil || !slices.Equal(target.Synonyms, []string{"Kingdom of Spain", "Catalonia", "Catalunya", "Asia"}) {
		t.Fatalf("merge: %+v %v", target, err)
	}
	if barcelona, err = r.Term(ctx, barcelona.ID); err != nil || barcelona.Path != "/world/spain/barcelona/" {
		t.Fatalf("merged child: %+v %v", barcelona, err)
	}
	if asia, err = r.Term(ctx, asia.ID); err != nil || asia.MergedIntoID != moved.ID {
		t.Fatalf("earlier merge: %+v %v", asia, err)
	}
	if canonical, err := r.Canonical(ctx, asia.ID); err != nil || canonical.ID != moved.ID {
		t.Fatalf("canonical: %+v %v", canonical, err)
	}
	family, err := r.Family(ctx, world.ID)
	slices.Sort(family)
	if err != nil || !slices.Equal(family, []string{"asia", "barcelona", "catalonia", "madrid", "spain", "world"}) {
		t.Fatalf("family: %v %v", family, err)
	}
	match, err := r.Match(ctx, []string{"spain", "europe", "asia"}, []string{"barcelona", "catalonia"})
	if err != nil || !match["spain"] || !match["asia"] || match["europe"] {
		t.Fatalf("match: %v %v", match, err)
	}

	// Keywords match names and synonyms without regard to case.
	tax := add(taxonomy.KeywordsSetID, "", "tax", "Tax", nil, "Levy")
	for _, name := range []string{"TAX", "levy"} {
		found, _, ok, err := r.Keyword(ctx, name)
		if err != nil || !ok || found.ID != tax.ID {
			t.Fatalf("keyword %s: %+v %v %v", name, found, ok, err)
		}
	}
	if _, _, ok, err := r.Keyword(ctx, "Spain"); err != nil || ok {
		t.Fatalf("a term of another set is a keyword: %v %v", ok, err)
	}

	// Sets that hold terms or that fields use stay.
	if err = r.DeleteSet(ctx, places.ID, places.Version); !taxonomy.IsViolation(err) {
		t.Fatalf("deleted a set with terms: %v", err)
	}
	empty := daltest.TermSet(t, s, "empty")
	if _, err = s.Catalog().CreateField(ctx, catalog.FieldDefinition{Key: "topic", Label: "Topic", Type: catalog.FieldTerm, Options: model.FieldOptions{TermSetID: empty.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = r.DeleteSet(ctx, empty.ID, empty.Version); !taxonomy.IsViolation(err) {
		t.Fatalf("deleted a set a field uses: %v", err)
	}
	if err = r.DeleteGroup(ctx, taxonomy.SystemGroupID, 1); !taxonomy.IsViolation(err) {
		t.Fatalf("deleted the system group: %v", err)
	}
}
