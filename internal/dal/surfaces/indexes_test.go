package surfaces_test

import (
	"context"
	"papergo/internal/dal/daltest"
	"strings"
	"testing"
)

// Queries by a collection surface's modifier read the modifier index in
// item order instead of sorting the collection.
func TestSurfaceModifierIndex_SQLite(t *testing.T) {
	s := daltest.SQLiteStore(t)
	rows, err := s.SQLDB().QueryContext(context.Background(), "EXPLAIN QUERY PLAN SELECT item_id FROM item_surfaces WHERE container_id=? AND surface=? AND modified_by=? ORDER BY item_id", "list", "head", "editor")
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
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "itemsurface_container_id_surface_modified_by_item_id") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}
