package database

import (
	"context"
	"path/filepath"
	"testing"
)

// Cached statements serve repeated and nested uses of the same SQL, and a
// statement that failed is prepared again.
func TestStatementCache(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.SQL.ExecContext(ctx, `CREATE TABLE things (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, name := range []string{"a", "b", "c"} {
		if _, err = tx.ExecContext(ctx, `INSERT INTO things (name) VALUES (?)`, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO things (name) VALUES (?)`, "a"); err == nil {
		t.Fatal("a duplicate name was inserted")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO things (name) VALUES (?)`, "d"); err != nil {
		t.Fatalf("the statement after a failure: %v", err)
	}
	// The same query while its rows are open runs on a statement of its own.
	const query = `SELECT name FROM things WHERE id > ? ORDER BY id`
	outer, err := tx.QueryContext(ctx, query, 0)
	if err != nil {
		t.Fatal(err)
	}
	pairs := 0
	for outer.Next() {
		var name string
		if err = outer.Scan(&name); err != nil {
			t.Fatal(err)
		}
		inner, err := tx.QueryContext(ctx, query, 2)
		if err != nil {
			t.Fatal(err)
		}
		for inner.Next() {
			pairs++
		}
		if err = inner.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err = outer.Err(); err != nil || pairs != 8 {
		t.Fatalf("%d pairs: %v", pairs, err)
	}
	if err = outer.Close(); err != nil {
		t.Fatal(err)
	}
	var n int
	for range 3 {
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM things WHERE id > ?`, 0).Scan(&n); err != nil || n != 4 {
			t.Fatal(n, err)
		}
	}
}
