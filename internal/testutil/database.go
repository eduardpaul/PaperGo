package testutil

import (
	"ariga.io/atlas/sql/migrate"
	"context"
	"os"
	"papergo/internal/database"
	"path/filepath"
	"runtime"
	"testing"
)

func Database(t *testing.T) *database.Database {
	return DatabaseThrough(t, "")
}

func DatabaseThrough(t *testing.T, version string) *database.Database {
	t.Helper()
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	files := MigrationFiles(t)
	for _, f := range files {
		if version != "" && f.Version() > version {
			break
		}
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

func MigrationFiles(t *testing.T) []migrate.File {
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

func ReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
