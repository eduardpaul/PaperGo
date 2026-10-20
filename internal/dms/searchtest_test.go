package dms

import (
	"papergo/internal/dal/refs"
	"papergo/internal/dal/resources"
	"path/filepath"
	"testing"
	"time"

	"papergo/internal/search"
	"papergo/internal/testutil/searchstore"
)

// indexSearch gives the service a search store, when it has none, and
// indexes every live item and folder, as the search.index workflow keeps it
// in a running PaperGo: tests call it after their writes and before they
// search.
func indexSearch(t testing.TB, s *Service) {
	t.Helper()
	if s.search == nil {
		store := searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})
		s.SetSearch(store)
	}
	ids, err := s.DAL.Resources().IDs(testContext, resources.Filter{Kinds: []refs.ResourceKind{refs.KindItem, refs.KindFolder}, State: resources.Live})
	if err != nil {
		t.Fatal(err)
	}
	docs, _, err := s.SearchDocuments(testContext, ids)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	if err = s.search.UpsertDocuments(testContext, docs, stamp); err != nil {
		t.Fatal(err)
	}
	if err = s.search.DeleteDocuments(testContext, search.Selection{Before: stamp}); err != nil {
		t.Fatal(err)
	}
}
