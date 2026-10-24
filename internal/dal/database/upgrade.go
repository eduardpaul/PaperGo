package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"papergo/internal/dal"
)

// upgradeLock is the PostgreSQL advisory lock that serializes upgrades
// between instances.
const upgradeLock int64 = 0x7061706572676f // "papergo"

// Upgraded reports what Upgrade did.
type Upgraded struct {
	Created  bool   // installed the schema master into an empty database
	From, To int    // schema versions before and after
	Snapshot string // SQLite copy taken before migrating; empty otherwise
}

// Upgrade brings the database to this build's schema version. An empty
// database gets the schema master; an older one gets every pending
// migration in one transaction, so a failure changes nothing. A database
// newer than this build is refused. SQLite callers hold the exclusive lease.
func (d *Database) Upgrade(ctx context.Context) (Upgraded, error) {
	return d.upgrade(ctx, dal.Migrations(d.DAL.Dialect()))
}

func (d *Database) upgrade(ctx context.Context, steps []dal.Migration) (Upgraded, error) {
	if d.DAL.Dialect() == dal.DialectPostgres {
		return d.upgradePostgres(ctx, steps)
	}
	return d.upgradeSQLite(ctx, steps)
}

// pending returns the steps after installed, refusing a database newer
// than the last step.
func pending(installed int, steps []dal.Migration) ([]dal.Migration, error) {
	if installed > len(steps) {
		return nil, fmt.Errorf("database schema version %d is newer than this PaperGo's %d; downgrades are unsupported", installed, len(steps))
	}
	return steps[installed:], nil
}

func (d *Database) upgradePostgres(ctx context.Context, steps []dal.Migration) (result Upgraded, err error) {
	tx, err := d.SQL.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, upgradeLock); err != nil {
		return result, err
	}
	store := d.DAL.WithTx(tx)
	exists, err := store.HasTables(ctx)
	if err != nil {
		return result, err
	}
	if !exists {
		if err = store.Migrate(ctx); err != nil {
			return result, err
		}
		result = Upgraded{Created: true, From: dal.SchemaVersion(), To: dal.SchemaVersion()}
		return result, tx.Commit()
	}
	if result.From, err = store.InstalledVersion(ctx); err != nil {
		return result, err
	}
	todo, err := pending(result.From, steps)
	if err != nil {
		return result, err
	}
	result.To = result.From
	if len(todo) == 0 {
		return result, nil
	}
	for _, m := range todo {
		if _, err = tx.ExecContext(ctx, m.SQL); err != nil {
			return result, fmt.Errorf("migration %s: %w", m.Name, err)
		}
	}
	last := todo[len(todo)-1].Version
	if _, err = tx.ExecContext(ctx, `UPDATE schema_metadata SET version=$1 WHERE id=1`, last); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, fmt.Errorf("upgrade rolled back: %w", err)
	}
	result.To = last
	return result, nil
}

func (d *Database) upgradeSQLite(ctx context.Context, steps []dal.Migration) (result Upgraded, err error) {
	exists, err := d.DAL.HasTables(ctx)
	if err != nil {
		return result, err
	}
	if !exists {
		if err = d.DAL.Migrate(ctx); err != nil {
			return result, err
		}
		return Upgraded{Created: true, From: dal.SchemaVersion(), To: dal.SchemaVersion()}, nil
	}
	if result.From, err = d.DAL.InstalledVersion(ctx); err != nil {
		return result, err
	}
	result.To = result.From
	todo, err := pending(result.From, steps)
	if err != nil || len(todo) == 0 {
		return result, err
	}
	if result.Snapshot, err = d.snapshotBeforeUpgrade(ctx); err != nil {
		return result, err
	}
	conn, err := d.SQL.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if err = migrateSQLite(ctx, conn, todo); err != nil {
		return result, fmt.Errorf("upgrade rolled back (snapshot %s): %w", result.Snapshot, err)
	}
	result.To = todo[len(todo)-1].Version
	return result, nil
}

// snapshotBeforeUpgrade copies the database beside it. VACUUM INTO includes
// committed WAL content, which copying the file alone would miss.
func (d *Database) snapshotBeforeUpgrade(ctx context.Context) (string, error) {
	root, err := os.MkdirTemp(filepath.Dir(d.Path), "papergo-pre-upgrade-")
	if err != nil {
		return "", fmt.Errorf("create upgrade snapshot directory: %w", err)
	}
	snapshot := filepath.Join(root, "database.sqlite")
	if _, err = d.SQL.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		os.RemoveAll(root)
		return "", fmt.Errorf("snapshot database before upgrade: %w", err)
	}
	f, err := os.OpenFile(snapshot, os.O_RDWR, 0)
	if err != nil {
		return snapshot, err
	}
	err = f.Sync()
	return snapshot, errors.Join(err, f.Close())
}

// migrateSQLite runs the steps in one transaction on one connection. Table
// rebuilds need foreign keys off, which SQLite only allows outside a
// transaction, so every reference and the file's integrity are checked
// after each step instead, before COMMIT.
func migrateSQLite(ctx context.Context, conn *sql.Conn, steps []dal.Migration) (err error) {
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
	for _, m := range steps {
		if _, err = conn.ExecContext(ctx, m.SQL); err != nil {
			return fmt.Errorf("migration %s: %w", m.Name, err)
		}
		var rows *sql.Rows
		if rows, err = conn.QueryContext(ctx, "PRAGMA foreign_key_check"); err != nil {
			return err
		}
		bad := rows.Next()
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if bad {
			return fmt.Errorf("migration %s violates foreign key constraints", m.Name)
		}
		var integrity string
		if err = conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
			return err
		}
		if integrity != "ok" {
			return fmt.Errorf("migration %s integrity check: %s", m.Name, integrity)
		}
	}
	if _, err = conn.ExecContext(ctx, "UPDATE schema_metadata SET version=? WHERE id=1", steps[len(steps)-1].Version); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}
