package database_test

import (
	"papergo/internal/dal/daltest"
	"path/filepath"
	"strings"
	"testing"
)

// The keyset reads that page resources, relationships and keywords walk an
// index in order instead of sorting, and revision collection finds the
// events that keep a revision without scanning the undispatched ones.
func TestReadPlansUseIndexes(t *testing.T) {
	db := daltest.DatabaseAt(t, filepath.Join(t.TempDir(), "papergo.db"))
	for _, tc := range []struct {
		query, index string
		args         []any
	}{
		{"SELECT id FROM resources WHERE workspace_id=? AND kind='item' AND id>? ORDER BY id LIMIT 100", "resource_workspace_id_kind_id", []any{"workspace", ""}},
		{"SELECT id FROM relationships WHERE source_id=? AND type_id IS NULL AND id>? ORDER BY id LIMIT 100", "relationship_source_id_type_id_id", []any{"item", ""}},
		{"SELECT id FROM relationships WHERE target_id=? AND type_id IS NULL AND id>? ORDER BY id LIMIT 100", "relationship_target_id_type_id_id", []any{"item", ""}},
		{"SELECT resource_id FROM resource_keywords WHERE keyword=?", "PRIMARY KEY", []any{"legal"}},
		{"SELECT 1 FROM domain_events e WHERE json_extract(e.data, '$.revision_id')=? AND e.dispatched_at IS NULL", "domainevent_revision_id", []any{"revision"}},
		{"SELECT 1 FROM item_revisions n JOIN domain_events e ON json_extract(e.data, '$.revision_id') = n.id AND e.dispatched_at IS NULL WHERE n.item_id=? AND n.revision_number=?", "domainevent_revision_id", []any{"item", 2}},
	} {
		rows, err := db.SQL.Query("EXPLAIN QUERY PLAN "+tc.query, tc.args...)
		if err != nil {
			t.Fatal(err)
		}
		details := ""
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			details += detail
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(details, tc.index) || strings.Contains(details, "TEMP B-TREE") {
			t.Fatalf("query misses read index %s: %s", tc.index, details)
		}
	}
}
