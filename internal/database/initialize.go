package database

import (
	"ariga.io/atlas/sql/migrate"
	atl "ariga.io/atlas/sql/sqlite"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Initialize creates the current schema in a fresh database. Existing databases
// must already match that schema; initialization never upgrades or repairs them.
func Initialize(ctx context.Context, path, schemaDir string) error {
	dir, err := migrate.NewLocalDir(schemaDir)
	if err != nil {
		return err
	}
	if err = migrate.Validate(dir); err != nil {
		return err
	}
	sum, err := dir.Checksum()
	if err != nil {
		return err
	}
	expected, err := sum.SumByName("20261007000100_schema.sql")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	fresh := err == nil
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if fresh {
		if err = file.Close(); err != nil {
			return err
		}
	}
	lease, err := AcquireLease(path, false)
	if err != nil {
		return err
	}
	defer lease.Close()
	db, err := Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	if fresh {
		if _, err = db.SQL.ExecContext(ctx, `CREATE TABLE atlas_schema_revisions(version TEXT PRIMARY KEY,applied INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL,hash TEXT NOT NULL)`); err != nil {
			return err
		}
		driver, err := atl.Open(db.SQL)
		if err != nil {
			return err
		}
		executor, err := migrate.NewExecutor(driver, dir, &schemaRevisions{db: db.SQL})
		if err != nil {
			return err
		}
		if err = executor.ExecuteN(ctx, 0); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	}
	if err = db.CheckSchema(ctx); err != nil {
		return err
	}
	var actual string
	if err = db.SQL.QueryRowContext(ctx, "SELECT hash FROM atlas_schema_revisions WHERE version='20261007000100'").Scan(&actual); err != nil {
		return fmt.Errorf("read schema checksum: %w", err)
	}
	if actual != expected {
		return errors.New("database schema checksum differs from this application; use a fresh database")
	}
	return nil
}

type schemaRevisions struct{ db *sql.DB }

func (*schemaRevisions) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: "atlas_schema_revisions"}
}
func (r *schemaRevisions) ReadRevision(ctx context.Context, version string) (*migrate.Revision, error) {
	v := &migrate.Revision{}
	err := r.db.QueryRowContext(ctx, "SELECT version,applied,total,error,hash FROM atlas_schema_revisions WHERE version=?", version).Scan(&v.Version, &v.Applied, &v.Total, &v.Error, &v.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, migrate.ErrRevisionNotExist
	}
	return v, err
}
func (r *schemaRevisions) ReadRevisions(ctx context.Context) ([]*migrate.Revision, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT version,applied,total,error,hash FROM atlas_schema_revisions ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*migrate.Revision
	for rows.Next() {
		v := &migrate.Revision{}
		if err = rows.Scan(&v.Version, &v.Applied, &v.Total, &v.Error, &v.Hash); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (r *schemaRevisions) WriteRevision(ctx context.Context, v *migrate.Revision) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO atlas_schema_revisions(version,applied,total,error,hash) VALUES(?,?,?,?,?) ON CONFLICT(version) DO UPDATE SET applied=excluded.applied,total=excluded.total,error=excluded.error,hash=excluded.hash`, v.Version, v.Applied, v.Total, v.Error, v.Hash)
	return err
}
func (r *schemaRevisions) DeleteRevision(ctx context.Context, version string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM atlas_schema_revisions WHERE version=?", version)
	return err
}
