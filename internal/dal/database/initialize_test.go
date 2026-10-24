package database_test

import (
	"fmt"
	"papergo/internal/dal"
	"papergo/internal/dal/database"
	"path/filepath"
	"testing"
)

func TestInitializeCurrentSchemaAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	ctx := t.Context()
	if err := database.Initialize(ctx, path); err != nil {
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
	if err = database.Initialize(ctx, path); err != nil {
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
	if _, err = db.SQL.ExecContext(ctx, fmt.Sprintf("UPDATE schema_metadata SET version=%d", dal.SchemaVersion()+1)); err != nil {
		t.Fatal(err)
	}
	if err = database.Initialize(ctx, path); err == nil {
		t.Fatal("newer schema accepted")
	}
}

func TestInitializeRefusesARunningServer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "papergo.db")
	lease, err := database.AcquireLease(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = database.Initialize(t.Context(), path); err == nil {
		t.Fatal("initialized a database in use")
	}
}
