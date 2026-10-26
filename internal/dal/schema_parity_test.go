package dal_test

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"papergo/internal/dal/daltest"
)

// The schema masters declare the same foreign keys, with the same delete
// actions, on both engines.
func TestEnginesDeclareTheSameForeignKeys(t *testing.T) {
	ctx := context.Background()
	postgres := daltest.PostgresStore(t)
	sqlite := daltest.SQLiteStore(t)
	want, err := keys(ctx, postgres.SQLDB(), `SELECT tc.table_name, kcu.column_name, ccu.table_name, rc.delete_rule
 FROM information_schema.table_constraints tc
 JOIN information_schema.key_column_usage kcu ON kcu.constraint_name = tc.constraint_name AND kcu.table_schema = tc.table_schema
 JOIN information_schema.referential_constraints rc ON rc.constraint_name = tc.constraint_name AND rc.constraint_schema = tc.table_schema
 JOIN information_schema.constraint_column_usage ccu ON ccu.constraint_name = tc.constraint_name AND ccu.table_schema = tc.table_schema
 WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_schema = current_schema()`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := keys(ctx, sqlite.SQLDB(), `SELECT m.name, k."from", k."table", k.on_delete
 FROM sqlite_master m, pragma_foreign_key_list(m.name) k WHERE m.type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range want {
		if !slices.Contains(got, key) {
			t.Errorf("SQLite lacks %s", key)
		}
	}
	for _, key := range got {
		if !slices.Contains(want, key) {
			t.Errorf("PostgreSQL lacks %s", key)
		}
	}
}

// keys lists foreign keys as "table.column -> target ON DELETE action".
func keys(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var table, column, target, action string
		if err := rows.Scan(&table, &column, &target, &action); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s.%s -> %s ON DELETE %s", table, column, target, action))
	}
	return out, rows.Err()
}
