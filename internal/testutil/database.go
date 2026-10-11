package testutil

import (
	"ariga.io/atlas/sql/migrate"
	"context"
	"papergo/internal/database"
	"papergo/internal/identity"
	"path/filepath"
	"runtime"
	"testing"
)

func Database(t testing.TB) *database.Database {
	t.Helper()
	return DatabaseAt(t, filepath.Join(t.TempDir(), "test.db"))
}

// DatabaseAt opens the database file at path and applies the schema to it.
func DatabaseAt(t testing.TB, path string) *database.Database {
	t.Helper()
	db, err := database.Open(context.Background(), path)
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
	directory := identity.Directory{DB: db.SQL}
	if err = directory.Initialize(context.Background(), "organization-admin"); err != nil {
		t.Fatal(err)
	}
	// Content fixtures exercise ACL ownership independently of organization-wide
	// administration. Alice can curate schemas/taxonomy but has no workspace.manage.
	for _, subject := range []string{"alice", "bob", "carol", "dave", "mallory", "writer", "reader", "visitor", "member", "owner", "other", "eve", "admin", "local-admin", "delegate", "editor", "outsider", "stranger", "publisher", "rita"} {
		if err = directory.EnsureUser(context.Background(), subject); err != nil {
			t.Fatal(err)
		}
	}
	role, err := directory.SaveRole(context.Background(), "organization-admin", "", 0, identity.RoleInput{Name: "Fixture curator", Scopes: []string{"contentType.manage", "taxonomy.manage", "user.read", "user.manage", "group.read", "group.manage", "role.read", "role.manage", "audit.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(context.Background(), "organization-admin", role.ID, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
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
