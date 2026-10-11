// docker-smoke-db creates a fresh database fixture for the Docker startup check.
// Use PaperGo's SQLite driver so FTS5 support matches the image being tested.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"papergo/internal/database"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/sqlite"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: docker-smoke-db <new database path>")
	}
	if err := create(context.Background(), os.Args[1], "migrations"); err != nil {
		log.Fatal(err)
	}
}

func create(ctx context.Context, path, migrationDir string) error {
	// Never apply this fixture tool to an existing database.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	db, err := database.Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()
	dir, err := migrate.NewLocalDir(migrationDir)
	if err != nil {
		return err
	}
	drv, err := sqlite.Open(db.SQL)
	if err != nil {
		return err
	}
	// This disposable fixture persists the executor's progress fields checked by
	// database.CheckSchema. The executor validates atlas.sum and executes the SQL.
	if _, err := db.SQL.ExecContext(ctx, `CREATE TABLE atlas_schema_revisions (
		version TEXT PRIMARY KEY, applied INTEGER NOT NULL,
		total INTEGER NOT NULL, error TEXT NOT NULL
	)`); err != nil {
		return err
	}
	executor, err := migrate.NewExecutor(drv, dir, &fixtureRevisions{db: db.SQL})
	if err != nil {
		return err
	}
	if err := executor.ExecuteN(ctx, 0); err != nil {
		return fmt.Errorf("apply smoke database schema: %w", err)
	}
	return db.CheckSchema(ctx)
}

// The fixture starts empty and is discarded after the smoke check, so reads and
// deletes use Atlas's empty revision store; writes retain real execution progress.
type fixtureRevisions struct {
	migrate.NopRevisionReadWriter
	db *sql.DB
}

func (*fixtureRevisions) Ident() *migrate.TableIdent {
	return &migrate.TableIdent{Name: "atlas_schema_revisions"}
}

func (r *fixtureRevisions) WriteRevision(ctx context.Context, revision *migrate.Revision) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO atlas_schema_revisions (version, applied, total, error)
		VALUES (?, ?, ?, ?) ON CONFLICT(version) DO UPDATE SET
		applied=excluded.applied, total=excluded.total, error=excluded.error`,
		revision.Version, revision.Applied, revision.Total, revision.Error)
	return err
}
