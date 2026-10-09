package dms

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

// The index-driven pages must return exactly the pages of the sorting CTE,
// across surfaces, item-level grants, missing values, ties and both directions.
func TestSeekPagesMatchSortedEligibleRows(t *testing.T) {
	s, w, list := fixture(t)
	readers(t, s, w)
	for _, f := range []CreateField{{Key: "n", Label: "N", Type: "integer", Indexed: true}, {Key: "label", Label: "Label", Type: "text", Indexed: true}, {Key: "x", Label: "X", Type: "number", Indexed: true}} {
		if _, err := s.CreateField(testContext, "alice", list.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 24; i++ {
		values := map[string]any{}
		if i%5 != 0 {
			values["n"] = num(i % 4)
		}
		if i%3 != 0 {
			values["label"] = []string{"b", "a", "c"}[i%3]
		}
		if i%4 != 0 {
			values["x"] = json.Number(fmt.Sprintf("%d.5", i%3))
		}
		name := fmt.Sprintf("item %02d", i%7)
		tags := []string{}
		if i%2 == 0 {
			tags = []string{"even"}
		}
		r, err := s.Create(testContext, "alice", list.ID, CreateResource{Kind: "item", Name: name, Tags: tags, Values: values})
		if err != nil {
			t.Fatal(err)
		}
		if i%3 != 1 {
			if _, err = s.Publish(testContext, "alice", r.ID, r.Version); err != nil {
				t.Fatal(err)
			}
			if i%4 == 2 {
				r = latest(t, s, r.ID)
				changed, edited := map[string]any{"n": num(9 - i%4), "label": "z"}, "edited"
				if _, err = s.Update(testContext, "alice", r.ID, r.Version, UpdateResource{Name: &edited, Values: &changed}); err != nil {
					t.Fatal(err)
				}
			}
		}
		switch i % 6 {
		case 1: // the reader may see this item's drafts
			r = latest(t, s, r.ID)
			if _, err = s.SetPermissions(testContext, "alice", r.ID, r.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}, {"reader", "read_draft", "allow"}}}); err != nil {
				t.Fatal(err)
			}
		case 4: // the reader may not see this item at all
			r = latest(t, s, r.ID)
			if _, err = s.SetPermissions(testContext, "alice", r.ID, r.Version, Permissions{Grants: []Permission{{"alice", "manage", "allow"}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	specs := []QuerySpec{
		{},
		{Filter: &FilterExpr{Field: "n", Op: "gte", Value: json.RawMessage("1")}},
		{Tag: "even"},
		{Search: "item"},
	}
	sorts := []string{"", "$name", "$modified_at", "$created_by", "n", "label", "x"}
	compared := 0
	for _, subject := range []string{"alice", "reader"} {
		for _, surface := range []string{"auto", "head", "published"} {
			for _, base := range specs {
				for _, field := range sorts {
					for _, direction := range []string{"asc", "desc"} {
						spec := base
						spec.Sort = SortSpec{Field: field, Direction: direction}
						seek, sorted := pageAll(t, s, subject, list.ID, surface, spec, true), pageAll(t, s, subject, list.ID, surface, spec, false)
						if !reflect.DeepEqual(seek, sorted) {
							t.Fatalf("%s/%s %+v sort %s %s:\n seek   %v\n sorted %v", subject, surface, base, field, direction, seek, sorted)
						}
						compared += len(seek)
					}
				}
			}
		}
	}
	if compared == 0 {
		t.Fatal("fixture produced no rows; comparison would be vacuous")
	}
}

// pageAll collects every page of a query, at most three rows per page.
func pageAll(t *testing.T, s *Service, subject, collection, surface string, spec QuerySpec, seek bool) []string {
	t.Helper()
	ids := []string{}
	in := QueryRequest{Query: spec, Surface: surface, Limit: 3}
	for {
		out, err := read(testContext, s, func(tx *Service) (QueryResult, error) {
			q, err := tx.compileQuery(testContext, subject, collection, in, false)
			if err != nil {
				return QueryResult{}, err
			}
			if !seek {
				q.seek = nil
			}
			return tx.queryCompiled(testContext, subject, in, q)
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range out.Data {
			ids = append(ids, r.ID[len(r.ID)-4:]+":"+r.Name)
		}
		if out.NextCursor == "" {
			return ids
		}
		in.After = out.NextCursor
	}
}
