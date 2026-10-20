package dal_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"papergo/internal/dal"
)

func freshStore(t *testing.T) *dal.Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "schema.sqlite")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return dal.NewStore(db)
}

func TestMigrateAtomic(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	// Fail midway through DDL after the identity tables have been created.
	if _, err := s.SQLDB().ExecContext(ctx, `CREATE TABLE terms (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("accepted an existing table")
	}
	var count int
	if err := s.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE 'identity_%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed schema creation left %d tables", count)
	}
}

func TestMigrateJoinsExternalTransaction(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tx, err := s.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := s.WithTx(tx).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var inside int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM term_sets`).Scan(&inside); err != nil || inside != 1 {
		t.Fatalf("schema not in transaction: %d, %v", inside, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var outside int
	if err := s.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&outside); err != nil {
		t.Fatal(err)
	}
	if outside != 0 {
		t.Fatalf("schema escaped rollback: %d tables", outside)
	}
}
