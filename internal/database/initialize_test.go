package database_test

import (
	"os"
	"papergo/internal/database"
	"path/filepath"
	"testing"
)

func TestInitializeCurrentSchemaAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	ctx := t.Context()
	if err := database.Initialize(ctx, path, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SQL.ExecContext(ctx, "INSERT INTO identity_users(id,created_at,display_name) VALUES('persisted',CURRENT_TIMESTAMP,'Preserved')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err = database.Initialize(ctx, path, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.SQL.QueryRowContext(ctx, "SELECT display_name FROM identity_users WHERE id='persisted'").Scan(&name); err != nil || name != "Preserved" {
		t.Fatal(name, err)
	}
	if _, err = db.SQL.ExecContext(ctx, "UPDATE atlas_schema_revisions SET hash='invalid'"); err != nil {
		t.Fatal(err)
	}
	if err = database.Initialize(ctx, path, "../../migrations"); err == nil {
		t.Fatal("invalid schema checksum accepted")
	}
}
func TestInitializeRejectsExistingIncompleteDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := database.Initialize(t.Context(), path, "../../migrations"); err == nil {
		t.Fatal("incomplete database was repaired")
	}
}
