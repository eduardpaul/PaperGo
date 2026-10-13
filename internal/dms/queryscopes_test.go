package dms

import (
	"slices"
	"testing"
)

// Deciding permissions per ACL scope must return exactly what the row-by-row check returns.
func TestScopeDecisionsMatchRowChecks(t *testing.T) {
	s, w, c := fixture(t)
	if err := s.SetWorkspaceMember(testContext, "alice", w.ID, WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	published := func(name string) string {
		r := create(t, s, c.ID, "item", name, nil)
		if _, err := s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(testContext, "alice", r.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.ID
	}
	published("Public invoice")
	create(t, s, c.ID, "item", "Draft invoice", nil)
	for _, grants := range [][]Permission{
		{{"user", "alice", "manage", "allow"}, {"user", "reader", "read_draft", "allow"}},
		{{"user", "alice", "manage", "allow"}},
	} {
		id := published("Restricted invoice")
		r, err := s.Get(testContext, "alice", id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SetPermissions(testContext, "alice", id, r.Version, Permissions{Grants: grants}); err != nil {
			t.Fatal(err)
		}
	}
	gone, err := s.Get(testContext, "alice", published("Deleted invoice"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(testContext, "alice", gone.ID, gone.Version, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	folder := create(t, s, c.ID, "folder", "Archive", nil)
	create(t, s, folder.ID, "item", "Archived draft", nil)
	for _, subject := range []string{"alice", "reader"} {
		for _, surface := range []string{"auto", "head", "published"} {
			result := func(rowACL bool) ([]string, int) {
				q, err := s.Query(testContext, subject, c.ID, QueryRequest{Surface: surface, IncludeTotal: true, rowACL: rowACL})
				if err != nil {
					t.Fatal(subject, surface, err)
				}
				names := []string{}
				for _, r := range q.Data {
					names = append(names, r.Name)
				}
				slices.Sort(names)
				return names, counted(q.Total)
			}
			scoped, scopedTotal := result(false)
			rows, rowsTotal := result(true)
			if !slices.Equal(scoped, rows) || scopedTotal != rowsTotal || scopedTotal != len(rows) {
				t.Fatal(subject, surface, scoped, rows, scopedTotal, rowsTotal)
			}
		}
	}
	names, total := func() ([]string, int) {
		q, err := s.Query(testContext, "reader", c.ID, QueryRequest{IncludeTotal: true})
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, r := range q.Data {
			out = append(out, r.Name)
		}
		slices.Sort(out)
		return out, counted(q.Total)
	}()
	// The reader sees published items and the draft-readable restricted one, plus the folder.
	if total != 3 || !slices.Contains(names, "Public invoice") || !slices.Contains(names, "Restricted invoice") {
		t.Fatal("reader view", names, total)
	}
}

func TestTotalLimitCapsTheCount(t *testing.T) {
	s, _, c := fixture(t)
	for _, name := range []string{"One", "Two", "Three", "Four", "Five"} {
		create(t, s, c.ID, "item", name, nil)
	}
	for limit, want := range map[int][2]int{3: {3, 1}, 5: {5, 0}, 10: {5, 0}} {
		q, err := s.Query(testContext, "alice", c.ID, QueryRequest{TotalLimit: limit})
		capped := 0
		if q.TotalCapped {
			capped = 1
		}
		if err != nil || counted(q.Total) != want[0] || capped != want[1] {
			t.Fatal(limit, counted(q.Total), q.TotalCapped, err)
		}
	}
	if _, err := s.Query(testContext, "alice", c.ID, QueryRequest{TotalLimit: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
}
