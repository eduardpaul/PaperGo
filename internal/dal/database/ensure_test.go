package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEnsureSchemaOnEmptyDataDirectory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "papergo.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("empty data directory %s passed schema check after startup setup", path)
}
