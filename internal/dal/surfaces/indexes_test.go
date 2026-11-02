package surfaces_test

import (
	"context"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"
)

// sqlitePlan returns SQLite's query plan of a statement.
func sqlitePlan(t *testing.T, query string, args ...any) string {
	t.Helper()
	s := daltest.SQLiteStore(t)
	rows, err := s.SQLDB().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// Queries by a collection surface's modifier read the modifier index in
// item order instead of sorting the collection.
func TestSurfaceModifierIndex_SQLite(t *testing.T) {
	plan := sqlitePlan(t, "SELECT item_id FROM item_surfaces WHERE container_id=? AND surface=? AND modified_by=? ORDER BY item_id", "list", "head", "editor")
	if !strings.Contains(plan, "itemsurface_container_id_surface_modified_by_item_id") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}

// A sort by a field's values reads the one index holding them in order: the
// integer and boolean indexes are partial, so their sorts name the
// column's NOT NULL, and comparisons imply it; the text index holds every
// row. Term values are found by value through their partial index.
func TestValueIndexes_SQLite(t *testing.T) {
	for _, c := range []struct {
		column, notNull string
		args            []any
	}{{"value_integer", " AND value_integer IS NOT NULL", []any{int64(5)}}, {"value_text", "", []any{"x"}}, {"value_boolean", " AND value_boolean IS NOT NULL", []any{true}}} {
		index := "fieldvalue_container_id_surface_field_key_" + c.column + "_item_id"
		sorted := sqlitePlan(t, "SELECT item_id FROM field_values WHERE container_id=? AND surface=? AND field_key=? AND field_type=? AND scale=? AND ordinal=0"+c.notNull+" ORDER BY "+c.column+",item_id", "list", "head", "key", "text", 0)
		if !strings.Contains(sorted, index) || strings.Contains(sorted, "TEMP B-TREE") {
			t.Fatalf("sort by %s:\n%s", c.column, sorted)
		}
		compared := sqlitePlan(t, "SELECT item_id FROM field_values WHERE container_id=? AND surface IN ('head','published') AND field_key=? AND "+c.column+"=?", append([]any{"list", "key"}, c.args...)...)
		if !strings.Contains(compared, index) {
			t.Fatalf("compare %s:\n%s", c.column, compared)
		}
	}
	terms := sqlitePlan(t, "SELECT item_id FROM field_values f WHERE f.field_type=? AND f.field_type IN ('term','keywords') AND f.value_text IN (?,?)", "term", "a", "b")
	if !strings.Contains(terms, "fieldvalue_field_type_value_text_item_id") {
		t.Fatal(terms)
	}
}

// Removing a field deletes a collection's values of it through the text
// value index, which holds every row.
func TestCollectionValuesUseIndex_SQLite(t *testing.T) {
	plan := sqlitePlan(t, "DELETE FROM field_values WHERE container_id=? AND field_key=?", "list", "key")
	if strings.Contains(plan, "SCAN field_values") {
		t.Fatal(plan)
	}
}
