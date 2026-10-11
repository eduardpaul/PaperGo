package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ariga.io/atlas/sql/migrate"
	"papergo/internal/database"
)

func TestCreateAppliesFTS5AndTracksCompletedSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smoke.db")
	if err := create(t.Context(), path, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CheckSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := db.SQL.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE name IN ('resource_search','item_surface_search') AND sql LIKE '%USING fts5%'`).Scan(&tables); err != nil || tables != 2 {
		t.Fatalf("FTS5 tables: %d, error: %v", tables, err)
	}
	if err := create(t.Context(), path, "../../migrations"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing database was accepted: %v", err)
	}
}

func TestCreateRejectsInvalidChecksumAndTracksExecutionFailure(t *testing.T) {
	dir, err := migrate.NewLocalDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := dir.WriteFile("20261007000100_schema.sql", []byte("CREATE TABLE example(id INTEGER);\nSELECT missing_function();\n")); err != nil {
		t.Fatal(err)
	}
	if err := create(t.Context(), filepath.Join(t.TempDir(), "bad-checksum.db"), dir.Path()); !errors.Is(err, migrate.ErrChecksumNotFound) {
		t.Fatalf("missing checksum was accepted: %v", err)
	}
	sum, err := dir.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.WriteSumFile(dir, sum); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "failed.db")
	if err := create(t.Context(), path, dir.Path()); err == nil {
		t.Fatal("invalid SQL was accepted")
	}
	db, err := database.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var applied, total int
	var failure string
	if err := db.SQL.QueryRow("SELECT applied, total, error FROM atlas_schema_revisions").Scan(&applied, &total, &failure); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || total != 2 || failure == "" {
		t.Fatalf("incorrect failed migration state: %d/%d, error: %q", applied, total, failure)
	}
}
