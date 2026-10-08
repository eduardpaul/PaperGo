package testutil

import (
	"ariga.io/atlas/sql/migrate"
	"context"
	"papergo/internal/database"
	"path/filepath"
	"runtime"
	"testing"
)

func Database(t testing.TB) *database.Database {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	files := MigrationFiles(t)
	for _, f := range files {
		if _, err = db.SQL.ExecContext(context.Background(), string(f.Bytes())); err != nil {
			t.Fatalf("apply %s: %v", f.Name(), err)
		}
	}
	var foreignKeys int
	if err = db.SQL.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign keys disabled: %d %v", foreignKeys, err)
	}
	return db
}

func MigrationFiles(t testing.TB) []migrate.File {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	dir, err := migrate.NewLocalDir(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Validate(dir); err != nil {
		t.Fatal(err)
	}
	files, err := dir.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no versioned migrations")
	}
	return files
}
