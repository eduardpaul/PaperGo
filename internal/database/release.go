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
func ExecuteUpgrade(ctx context.Context, conn *sql.Conn, statements []string) error {
	return executeUpgradeSteps(ctx, conn, [][]string{statements})
}

func executeUpgradeSteps(ctx context.Context, conn *sql.Conn, steps [][]string) (err error) {
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
	for step, statements := range steps {
		for i, stmt := range statements {
			if _, err = conn.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("upgrade step %d statement %d: %w", step+1, i+1, err)
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
	if err = bundle.Validate(); err != nil {
		return "", err
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
	metadata := func(r migrations.Release) []string {
		return []string{
			fmt.Sprintf(`UPDATE atlas_schema_revisions SET hash='%s',applied=1,total=1,error='' WHERE version='20261007000100'`, r.Hash),
			fmt.Sprintf(`UPDATE papergo_release SET version='%s',schema_hash='%s' WHERE id=1`, r.Version, r.Hash),
		}
	}
	if tableCount == 0 {
		stmts := []string{current.Schema, `CREATE TABLE atlas_schema_revisions(version TEXT PRIMARY KEY,applied INTEGER NOT NULL,total INTEGER NOT NULL,error TEXT NOT NULL,hash TEXT NOT NULL)`, fmt.Sprintf(`INSERT INTO atlas_schema_revisions VALUES('20261007000100',1,1,'','%s')`, current.Hash),
			`CREATE TABLE papergo_release(id INTEGER PRIMARY KEY CHECK(id=1),version TEXT NOT NULL,schema_hash TEXT NOT NULL)`,
			fmt.Sprintf(`INSERT INTO papergo_release VALUES(1,'%s','%s')`, current.Version, current.Hash)}
		return "", ExecuteUpgrade(ctx, conn, stmts)
	}
	var version, hash string
	if err = conn.QueryRowContext(ctx, `SELECT r.version,r.schema_hash FROM papergo_release r JOIN atlas_schema_revisions a ON a.version='20261007000100' AND a.hash=r.schema_hash AND a.applied=a.total AND (a.error IS NULL OR a.error='') WHERE r.id=1`).Scan(&version, &hash); err != nil {
		return "", fmt.Errorf("database is not an initialized published schema: %w", err)
	}
	start := -1
	for i, r := range bundle.Releases {
		if r.Version == version && r.Hash == hash {
			start = i
			break
		}
	}
	if start < 0 {
		return "", errors.New("database release is not in this binary's published history; unknown versions and downgrades are unsupported")
	}
	if start == len(bundle.Releases)-1 {
		return "", nil
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
	var steps [][]string
	for _, r := range bundle.Releases[start+1:] {
		statements := append([]string{}, r.Statements...)
		steps = append(steps, append(statements, metadata(r)...))
	}
	if err = executeUpgradeSteps(ctx, conn, steps); err != nil {
		return snapshot, fmt.Errorf("release upgrade rolled back (snapshot %s): %w", snapshot, err)
	}
	return snapshot, nil
}

// Release startup can take longer than ordinary authentication initialization.
const ReleaseStartupTimeout = 10 * time.Minute
