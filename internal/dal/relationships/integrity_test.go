package relationships_test

import (
	"context"
	"errors"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/catalog"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/dberr"
	"papergo/internal/dal/refs"
	"papergo/internal/dal/relationships"
	"papergo/internal/dal/resources"
	"papergo/internal/dal/taxonomy"
	"sync"
	"testing"
)

func TestRelationshipCardinalityConcurrency(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		s := newStore(t)

		// Create relationship_types term set and predicate term
		tsRel := daltest.TermSet(t, s, taxonomy.RelationshipTypesSetKey)
		predTerm, err := s.Taxonomy().AddTerm(ctx, taxonomy.Term{ID: "term-supersedes", SetID: tsRel.ID, Name: "Supersedes"})
		if err != nil {
			t.Fatalf("AddTerm pred: %v", err)
		}
		maxOut := 1
		relType, err := s.Relationships().CreateType(ctx, relationships.Type{ID: predTerm.ID, InverseLabel: "Superseded by", Directed: true, MaxOutgoing: &maxOut})
		if err != nil {
			t.Fatalf("CreateType: %v", err)
		}

		ws, _ := s.Resources().Create(ctx, resources.New{
			ID:          "ws-rel",
			WorkspaceID: "ws-rel",
			Kind:        refs.KindWorkspace,
			Name:        "Rel WS",
		})
		lib, _ := s.Resources().Create(ctx, resources.New{
			ID:          "lib-rel",
			WorkspaceID: ws.ID,
			ParentID:    ws.ID,
			Kind:        refs.KindLibrary,
			Name:        "Rel Lib",
		})
		ct, _ := s.Catalog().CreateContentType(ctx, catalog.ContentType{ID: "ct-rel", Key: "doc", Name: "Doc"})
		_, _ = s.Catalog().Attach(ctx, lib.ID, ct.ID, true)

		srcItem, _ := s.Resources().Create(ctx, resources.New{
			ID:            "item-src",
			WorkspaceID:   ws.ID,
			ParentID:      lib.ID,
			ContainerID:   lib.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindItem,
			Name:          "Source",
		})
		dst1, _ := s.Resources().Create(ctx, resources.New{
			ID:            "item-dst-1",
			WorkspaceID:   ws.ID,
			ParentID:      lib.ID,
			ContainerID:   lib.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindItem,
			Name:          "Target 1",
		})
		dst2, _ := s.Resources().Create(ctx, resources.New{
			ID:            "item-dst-2",
			WorkspaceID:   ws.ID,
			ParentID:      lib.ID,
			ContainerID:   lib.ID,
			ContentTypeID: ct.ID,
			Kind:          refs.KindItem,
			Name:          "Target 2",
		})

		// Run two concurrent relationship creations from srcItem with max_outgoing = 1
		var wg sync.WaitGroup
		errs := make([]error, 2)
		targets := []string{dst1.ID, dst2.ID}
		for i := range 2 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, _, errs[idx] = s.Relationships().Link(ctx, relationships.Relationship{SourceID: srcItem.ID, TargetID: targets[idx], TypeID: relType.ID, Directed: true})
			}(i)
		}
		wg.Wait()

		successes := 0
		cardExceeded := 0
		for _, e := range errs {
			if e == nil {
				successes++
			} else if errors.Is(e, dberr.ErrCardinalityExceeded) {
				cardExceeded++
			} else {
				t.Fatalf("unexpected error in concurrent Link: %v", e)
			}
		}
		if successes != 1 || cardExceeded != 1 {
			t.Fatalf("expected 1 success and 1 ErrCardinalityExceeded, got successes=%d cardExceeded=%d", successes, cardExceeded)
		}
	})
}
