package revisions_test

import (
	"context"
	"fmt"
	_ "modernc.org/sqlite"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/revisions"
	"sync"
	"testing"
)

func TestConcurrentRevisions(t *testing.T) {
	daltest.Engines(t, func(t *testing.T, newStore func(testing.TB) *dal.Store) {
		ctx := context.Background()
		f := daltest.NewFixture(t, newStore(t))
		const n = 8
		var wg sync.WaitGroup
		results := make([]revisions.Revision, n)
		errs := make([]error, n)
		for i := range n {
			wg.Go(func() {
				results[i], errs[i] = daltest.Revise(ctx, f.Store, revisions.Revision{ItemID: f.Item, SchemaRevisionID: f.Schema, Name: fmt.Sprintf("Revision %d", i), CreatedBy: "alice"})
			})
		}
		wg.Wait()
		seen := map[int]bool{}
		for i, err := range errs {
			if err != nil {
				t.Fatalf("concurrent revision %d: %v", i, err)
			}
			if seen[results[i].RevisionNumber] {
				t.Fatalf("duplicate revision number: %d", results[i].RevisionNumber)
			}
			seen[results[i].RevisionNumber] = true
		}
		item, err := f.Store.Resources().Resource(ctx, f.Item)
		if err != nil || item.NextRevisionNumber != n+2 {
			t.Fatalf("revision sequence: %+v, %v", item, err)
		}
	})
}
