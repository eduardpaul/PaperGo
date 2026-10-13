package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"papergo/migrations"
)

// ExecuteUpgrade executes the entire pending release chain atomically. SQLite
// table rebuilds require disabling foreign keys before BEGIN, then checking
// every reference before COMMIT. It uses exactly one dedicated connection.
func ExecuteUpgrade(ctx context.Context, conn *sql.Conn, statements []string) (err error) {
	if _, err = conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		_, e := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
		err = errors.Join(err, e)
	}()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, e := conn.ExecContext(context.Background(), "ROLLBACK")
			err = errors.Join(err, e)
		}
	}()
	for i, stmt := range statements {
		if _, err = conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("upgrade statement %d: %w", i+1, err)
		}
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if bad {
		return errors.New("upgrade violates foreign key constraints")
	}
	var integrity string
	if err = conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("upgrade integrity check: %s", integrity)
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// ApplyRelease is called only by published binaries while holding the exclusive
// database lease, before the runner or any HTTP/background workers start.
func (d *Database) ApplyRelease(ctx context.Context, path string, bundle migrations.Bundle) (snapshot string, err error) {
	if len(bundle.Releases) == 0 {
		return "", errors.New("empty release bundle")
	}
	current := bundle.Releases[len(bundle.Releases)-1]
	var tableCount int
	if err = d.SQL.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tableCount); err != nil {
		return "", err
	}
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	metadata := func(hash string) string {
		return fmt.Sprintf(`UPDATE atlas_schema_revisions SET hash='%s',applied=1,total=1,error='' WHERE version='20261007000100'`, hash)
	}
	if tableCount == 0 {
		stmts := []string{current.Schema, `CREATE TABLE atlas_schema_revisions(version TEXT PRIMARY KEY,applied INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL,hash TEXT NOT NULL)`, fmt.Sprintf(`INSERT INTO atlas_schema_revisions VALUES('20261007000100',1,1,'','%s')`, current.Hash)}
		return "", ExecuteUpgrade(ctx, conn, stmts)
	}
	var hash string
	if err = conn.QueryRowContext(ctx, "SELECT hash FROM atlas_schema_revisions WHERE version='20261007000100' AND applied=total AND (error IS NULL OR error='')").Scan(&hash); err != nil {
		return "", fmt.Errorf("database is not an initialized published schema: %w", err)
	}
	if hash == current.Hash {
		return "", nil
	}
	start := -1
	for i, r := range bundle.Releases {
		if r.Hash == hash {
			start = i
		}
	}
	if start < 0 {
		return "", errors.New("database schema is not in this release's published history; development databases and downgrades are unsupported")
	}
	// VACUUM INTO includes committed WAL content; copying only the .db is unsafe.
	root, err := os.MkdirTemp(filepath.Dir(path), "papergo-pre-upgrade-")
	if err != nil {
		return "", fmt.Errorf("create upgrade snapshot directory: %w", err)
	}
	snapshot = filepath.Join(root, "database.sqlite")
	if _, err = conn.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("snapshot database before upgrade: %w", err)
	}
	f, err := os.OpenFile(snapshot, os.O_RDWR, 0)
	if err != nil {
		return snapshot, err
	}
	err = f.Sync()
	err = errors.Join(err, f.Close())
	if err != nil {
		return snapshot, err
	}
	var statements []string
	for _, r := range bundle.Releases[start+1:] {
		statements = append(statements, r.Statements...)
	}
	statements = append(statements, metadata(current.Hash))
	if err = ExecuteUpgrade(ctx, conn, statements); err != nil {
		return snapshot, fmt.Errorf("release upgrade rolled back (snapshot %s): %w", snapshot, err)
	}
	return snapshot, nil
}

// Release startup can take longer than ordinary authentication initialization.
const ReleaseStartupTimeout = 10 * time.Minute
