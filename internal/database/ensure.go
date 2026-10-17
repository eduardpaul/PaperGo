package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"ariga.io/atlas/sql/migrate"
	atl "ariga.io/atlas/sql/sqlite"
	"papergo/migrations"
)

// EnsureSchema applies the embedded current schema when the database has no
// tables, then checks that schema. An existing database is only checked.
// Published release upgrades stay on ApplyRelease.
func (d *Database) EnsureSchema(ctx context.Context) error {
	var tables int
	if err := d.SQL.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		if err := applyEmbeddedSchema(ctx, d.SQL); err != nil {
			return err
		}
	}
	return d.CheckSchema(ctx)
}

func applyEmbeddedSchema(ctx context.Context, sqlDB *sql.DB) error {
	dir, err := os.MkdirTemp("", "papergo-schema-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err = os.WriteFile(filepath.Join(dir, migrations.SchemaFile), []byte(migrations.SchemaSQL), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "atlas.sum"), []byte(migrations.ChecksumFile()), 0600); err != nil {
		return err
	}
	local, err := migrate.NewLocalDir(dir)
	if err != nil {
		return err
	}
	if err = migrate.Validate(local); err != nil {
		return err
	}
	if _, err = sqlDB.ExecContext(ctx, `CREATE TABLE atlas_schema_revisions(version TEXT PRIMARY KEY,applied INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL,hash TEXT NOT NULL)`); err != nil {
		return err
	}
	driver, err := atl.Open(sqlDB)
	if err != nil {
		return err
	}
	executor, err := migrate.NewExecutor(driver, local, &schemaRevisions{db: sqlDB})
	if err != nil {
		return err
	}
	if err = executor.ExecuteN(ctx, 0); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}
	return nil
}
