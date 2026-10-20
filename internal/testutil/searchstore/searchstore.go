// Package searchstore opens the selected real store for service/runner/API
// parity tests. CI runs these suites once per backend.
package searchstore

import (
	"context"
	"os"
	"testing"

	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/dal/search/zvecstore"
	"papergo/internal/search"
)

func Open(t testing.TB, path string, languages []string) search.Store {
	t.Helper()
	var store search.Store
	var err error
	switch os.Getenv("PAPERGO_TEST_SEARCH_STORE") {
	case "", "sqlite":
		store, err = sqlitestore.Open(context.Background(), path, sqlitestore.Options{Languages: languages})
	case "zvec":
		store, err = zvecstore.Open(context.Background(), path+".zvec", zvecstore.Options{Languages: languages})
	default:
		t.Fatal("PAPERGO_TEST_SEARCH_STORE must be sqlite or zvec")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}
