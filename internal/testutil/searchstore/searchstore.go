// Package searchstore opens a real search store for service/runner/API
// parity tests. The store follows the test database, as SEARCH_STORE does:
// SQLite beside a SQLite database, PostgreSQL in a PostgreSQL one.
// PAPERGO_TEST_SEARCH_STORE (sqlite or postgres) chooses another.
package searchstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/database"
	"papergo/internal/dal/search/pgstore"
	"papergo/internal/dal/search/sqlitestore"
	"papergo/internal/search"
)

// Name is the store Open opens for the database db.
func Name(db *database.Database) string {
	if name := os.Getenv("PAPERGO_TEST_SEARCH_STORE"); name != "" {
		return name
	}
	if db.URL != "" {
		return pgstore.Name
	}
	return sqlitestore.Name
}

// Open opens the search store of the test database db. A PostgreSQL store
// of a SQLite database lives in a PostgreSQL test database of its own.
func Open(t testing.TB, db *database.Database, languages []string) search.Store {
	t.Helper()
	ctx := context.Background()
	var store search.Store
	var err error
	switch Name(db) {
	case sqlitestore.Name:
		store, err = sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "search.db"), sqlitestore.Options{Languages: languages})
	case pgstore.Name:
		handle := db.SQL
		if db.URL == "" {
			handle = daltest.PostgresDB(t)
		}
		store, err = pgstore.Open(ctx, handle, pgstore.Options{Languages: languages})
	default:
		t.Fatal("PAPERGO_TEST_SEARCH_STORE must be sqlite or postgres")
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
