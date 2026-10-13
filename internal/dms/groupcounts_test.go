package dms

import (
	"fmt"
	"testing"
)

// Maintained choice counts must equal what the query counts row by row.
func TestGroupCountsMatchRowCounts(t *testing.T) {
	for _, publishing := range []bool{false, true} {
		t.Run(fmt.Sprintf("publishing=%v", publishing), func(t *testing.T) {
			s, w, _ := fixture(t)
			list, err := s.Create(testContext, "alice", w.ID, CreateResource{Kind: "list", Name: "Board", PublishingEnabled: publishing})
			if err != nil {
				t.Fatal(err)
			}
			status := fieldOK(t, s, list.ID, CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"Open", "Paid", "Late"}, Indexed: true})
			if err = s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
				t.Fatal(err)
			}
			compare := func(step string) {
				t.Helper()
				for _, subject := range []string{"alice", "reader"} {
					for _, surface := range []string{"auto", "head", "published"} {
						in := QueryRequest{Surface: surface, Query: QuerySpec{GroupBy: "status"}}
						fast, err := s.QueryGroups(testContext, subject, list.ID, in)
						if err != nil {
							t.Fatal(step, subject, surface, err)
						}
						in.rowACL = true
						rows, err := s.QueryGroups(testContext, subject, list.ID, in)
						if err != nil {
							t.Fatal(step, subject, surface, err)
						}
						if fmt.Sprint(fast.Data) != fmt.Sprint(rows.Data) {
							t.Fatal(step, subject, surface, fast.Data, rows.Data)
						}
					}
				}
			}
			compare("empty")
			items := []string{}
			for i, value := range []any{"Open", "Open", "Paid", nil, "Late", "Open"} {
				values := map[string]any{}
				if value != nil {
					values["status"] = value
				}
				items = append(items, create(t, s, list.ID, "item", fmt.Sprintf("Item %d", i), values).ID)
			}
			create(t, s, list.ID, "folder", "Folder", nil)
			compare("created")
			counts := func(subject string) bool {
				t.Helper()
				_, ok, err := s.countedGroups(testContext, subject, list.ID, QueryRequest{Query: QuerySpec{GroupBy: "status"}}, status)
				if err != nil {
					t.Fatal(err)
				}
				return ok
			}
			if !counts("alice") || !counts("reader") {
				t.Fatal("maintained counts not used")
			}
			patch := map[string]any{"status": "Paid"}
			if _, err = s.Update(testContext, "alice", items[0], latest(t, s, items[0]).Version, UpdateResource{Values: &patch}); err != nil {
				t.Fatal(err)
			}
			if publishing {
				if _, err = s.Publish(testContext, "alice", items[1], latest(t, s, items[1]).Version); err != nil {
					t.Fatal(err)
				}
			}
			compare("updated")
			if err = s.Delete(testContext, "alice", items[2], latest(t, s, items[2]).Version, DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			compare("deleted")
			// An item the reader cannot see makes their counts come from the rows.
			restricted := latest(t, s, items[4])
			if _, err = s.SetPermissions(testContext, "alice", restricted.ID, restricted.Version, Permissions{Grants: []Permission{{"user", "alice", "manage", "allow"}}}); err != nil {
				t.Fatal(err)
			}
			compare("restricted")
			if counts("reader") || !counts("alice") {
				t.Fatal("counts used for a reader who cannot see every item")
			}
			// Removing the field's index removes its counts with its index rows.
			off := false
			if _, err = s.UpdateField(testContext, "alice", list.ID, status.ID, latest(t, s, list.ID).Version, UpdateField{Indexed: &off}); err != nil {
				t.Fatal(err)
			}
			if err = s.runOperations(testContext); err != nil {
				t.Fatal(err)
			}
			var left int
			if err = s.scan(testContext, []any{&left}, "SELECT count(*) FROM group_counts WHERE container_id=? AND field_key='status'", list.ID); err != nil || left != 0 {
				t.Fatal("counts of an unindexed field", left, err)
			}
		})
	}
}
