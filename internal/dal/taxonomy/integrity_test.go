package taxonomy_test

import (
	"context"
	"errors"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/taxonomy"
	"sync"
	"testing"
)

func TestTaxonomyHierarchyCyclesAndMaterializedPath(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		ts := daltest.TermSet(t, s, "departments")
		terms := s.Taxonomy()
		tRoot, err := terms.AddTerm(ctx, taxonomy.Term{ID: "term-root", SetID: ts.ID, Name: "R&D", Color: "#1a2b3c"})
		if err != nil {
			t.Fatalf("AddTerm root: %v", err)
		}
		tChild, err := terms.AddTerm(ctx, taxonomy.Term{ID: "term-child", SetID: ts.ID, ParentID: tRoot.ID, Name: "Platform"})
		if err != nil {
			t.Fatalf("AddTerm child: %v", err)
		}
		tGrand, err := terms.AddTerm(ctx, taxonomy.Term{ID: "term-grand", SetID: ts.ID, ParentID: tChild.ID, Name: "Storage"})
		if err != nil {
			t.Fatalf("AddTerm grand: %v", err)
		}
		if tGrand.Path != "/term-root/term-child/term-grand/" {
			t.Fatalf("unexpected initial path %s", tGrand.Path)
		}

		// A term cannot move below its own subtree.
		if _, err = terms.Move(ctx, tRoot.ID, tRoot.Version, ts.ID, tGrand.ID, false); !taxonomy.IsViolation(err) {
			t.Fatalf("expected a taxonomy violation on a cycle, got %v", err)
		}

		// Moving tChild to the root rewrites tGrand's path.
		if _, err := terms.Move(ctx, tChild.ID, tChild.Version, ts.ID, "", false); err != nil {
			t.Fatalf("Move child to root: %v", err)
		}
		reloadedGrand, err := terms.Term(ctx, tGrand.ID)
		if err != nil {
			t.Fatalf("Term grand: %v", err)
		}
		if reloadedGrand.Path != "/term-child/term-grand/" {
			t.Fatalf("expected cascaded path /term-child/term-grand/, got %s", reloadedGrand.Path)
		}
	})
}

func TestConcurrentTaxonomyMoves(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)
		for _, id := range []string{"left", "right"} {
			if _, err := s.Taxonomy().AddTerm(ctx, taxonomy.Term{ID: id, Name: id, SetID: taxonomy.KeywordsSetID}); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Go(func() { _, errs[0] = s.Taxonomy().Move(ctx, "left", 1, taxonomy.KeywordsSetID, "right", false) })
		wg.Go(func() { _, errs[1] = s.Taxonomy().Move(ctx, "right", 1, taxonomy.KeywordsSetID, "left", false) })
		wg.Wait()
		daltest.AssertOneRejected(t, errs, taxonomy.Violation("a term cannot move below itself"))
	})
}

func TestKeywordCanonicalizationAndValidation(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		f := daltest.NewFixture(t, newStore(t))
		terms := f.Store.Taxonomy()
		for _, id := range []string{"first", "second", "third"} {
			if _, err := terms.AddTerm(ctx, taxonomy.Term{ID: id, Name: id, SetID: taxonomy.KeywordsSetID}); err != nil {
				t.Fatal(err)
			}
		}
		second, err := terms.Merge(ctx, "first", 1, "second", false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := terms.Merge(ctx, "second", second.Version, "first", false); !taxonomy.IsViolation(err) {
			t.Fatalf("merge cycle accepted: %v", err)
		}
		if _, err := terms.Merge(ctx, "second", second.Version, "third", false); err != nil {
			t.Fatal(err)
		}
		if err := daltest.Tag(ctx, f.Store, f.Item, []string{"first", "second", "third", " third "}); err != nil {
			t.Fatal(err)
		}
		item, err := f.Store.Resources().Resource(ctx, f.Item)
		if err != nil || len(item.Keywords) != 1 || item.Keywords[0] != "third" {
			t.Fatalf("noncanonical keywords: %+v, %v", item, err)
		}
		ts := daltest.TermSet(t, f.Store, "closed")
		term, err := terms.AddTerm(ctx, taxonomy.Term{ID: "unavailable", SetID: ts.ID, Name: "Unavailable"})
		if err != nil {
			t.Fatal(err)
		}
		if err := daltest.Tag(ctx, f.Store, f.Item, []string{term.ID}); !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("unavailable term accepted as keyword: %v", err)
		}
		daltest.Update(t, f.Store, "term_sets", daltest.Row{"is_keywords": false, "is_open": false}, daltest.Row{"id": taxonomy.KeywordsSetID})
		if err := daltest.Tag(ctx, f.Store, f.Item, []string{"new keyword"}); !errors.Is(err, dberr.ErrInvalidReference) {
			t.Fatalf("closed keyword set accepted new term: %v", err)
		}
	})
}
