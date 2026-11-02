package daltest

import (
	"context"
	"testing"

	"papergo/internal/dal"
	"papergo/internal/dal/database"
)

// Analyze brings PostgreSQL's planner statistics up to date, as autovacuum
// does a while after a bulk load, so a benchmark that has just seeded its
// tables times the plans a running server uses. SQLite plans without
// statistics, as PaperGo runs it.
func Analyze(tb testing.TB, db *database.Database) {
	tb.Helper()
	if db.DAL.Dialect() != dal.DialectPostgres {
		return
	}
	if _, err := db.SQL.ExecContext(context.Background(), "ANALYZE"); err != nil {
		tb.Fatal(err)
	}
}
