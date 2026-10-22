package dms

import (
	"encoding/base64"
	"encoding/json"
	"papergo/internal/dal/resources"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestMultiSortExactRanksMissingValuesProjectionAndPublication(t *testing.T) {
	s, w, l := fixture(t)
	grantReader(t, s, w)
	for _, f := range []CreateField{{Key: "bucket", Label: "Bucket", Type: "text", Indexed: true}, {Key: "serial", Label: "Serial", Type: "integer", Indexed: true}, {Key: "notes", Label: "Notes", Type: "note"}} {
		fieldOK(t, s, l.ID, f)
	}
	inputs := []struct{ name, bucket, serial string }{{"Zulu", "A", "9007199254740993"}, {"Bravo", "A", "9007199254740994"}, {"Alpha", "A", "9007199254740993"}, {"Charlie", "A", ""}, {"Echo", "B", "9007199254740994"}, {"Foxtrot", "", "9007199254740994"}, {"Golf", "", ""}, {"Alpha", "A", "9007199254740993"}}
	items := []*resources.Resource{}
	for _, in := range inputs {
		values := map[string]any{"notes": strings.Repeat("x", 16384)}
		if in.bucket != "" {
			values["bucket"] = in.bucket
		}
		if in.serial != "" {
			values["serial"] = json.Number(in.serial)
		}
		item, e := s.Create(testContext, "alice", l.ID, CreateResource{Kind: "item", Name: in.name, Keywords: []string{"keyword"}, Values: values})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.Publish(testContext, "alice", item.ID, item.Version); e != nil {
			t.Fatal(e)
		}
		items = append(items, item)
	}
	// The reader must sort and project the published value, not this larger draft.
	changed := map[string]any{"serial": json.Number("9007199254740995")}
	item := latest(t, s, items[0].ID)
	if _, e := s.Update(testContext, "alice", item.ID, item.Version, UpdateResource{Values: &changed}); e != nil {
		t.Fatal(e)
	}
	ties := []string{items[2].ID, items[7].ID}
	sort.Strings(ties)
	expected := []string{items[1].ID, ties[0], ties[1], items[0].ID, items[3].ID, items[4].ID, items[5].ID, items[6].ID}
	spec := QuerySpec{Sort: []SortSpec{{Field: "bucket", Direction: "asc"}, {Field: "serial", Direction: "desc"}, {Field: "$name", Direction: "asc"}}}
	in := QueryRequest{Query: spec, Select: []string{"bucket", "serial"}, Limit: 2, IncludeTotal: true}
	ids := []string{}
	firstCursor := ""
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not terminate")
		}
		out, e := s.Query(testContext, "reader", l.ID, in)
		if e != nil || out.Total == nil || *out.Total != 8 {
			t.Fatal(out, e)
		}
		for _, r := range out.Data {
			ids = append(ids, r.ID)
			if _, ok := r.Values["notes"]; ok || len(r.Keywords) != 0 {
				t.Fatal("selection leaked fields", r.Values, r.Keywords)
			}
		}
		if out.NextCursor == "" {
			break
		}
		if firstCursor == "" {
			firstCursor = out.NextCursor
		}
		in.After = out.NextCursor
	}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatal("wrong multi-key ordering", ids, expected)
	}
	// Descending primary order exercises NULL prefixes and ID ties in the
	// opposite direction, with a cursor after every item.
	ordered := []string{items[0].ID, items[1].ID, items[4].ID, items[5].ID, ties[1], ties[0], items[3].ID, items[6].ID}
	descending := QueryRequest{Surface: "head", Select: []string{}, Limit: 1, Query: QuerySpec{Sort: []SortSpec{{Field: "serial", Direction: "desc"}, {Field: "bucket", Direction: "asc"}}}}
	actual := []string{}
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("descending pagination did not terminate")
		}
		result, e := s.Query(testContext, "alice", l.ID, descending)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range result.Data {
			actual = append(actual, r.ID)
		}
		if result.NextCursor == "" {
			break
		}
		descending.After = result.NextCursor
	}
	if !reflect.DeepEqual(actual, ordered) {
		t.Fatal("descending multi-key order", actual, ordered)
	}
	head, e := s.Query(testContext, "alice", l.ID, QueryRequest{Query: spec, Select: []string{}, Surface: "head", Limit: 50})
	if e != nil || head.Data[0].ID != items[0].ID || len(head.Data[0].Values) != 0 {
		t.Fatal(head, e)
	}
	selected, e := s.Query(testContext, "reader", l.ID, QueryRequest{Select: []string{"notes", "$keywords"}, Limit: 1})
	if e != nil || len(selected.Data[0].Values) != 1 || len(selected.Data[0].Keywords) != 1 {
		t.Fatal(selected, e)
	}
	in.After = firstCursor
	in.Select = []string{"bucket"}
	if _, e = s.Query(testContext, "reader", l.ID, in); !isValidation(e) {
		t.Fatal("cursor reused with changed projection", e)
	}
	in.Select = []string{"bucket", "serial"}
	in.Query.Sort[1].Direction = "asc"
	if _, e = s.Query(testContext, "reader", l.ID, in); !isValidation(e) {
		t.Fatal("cursor reused with changed secondary direction", e)
	}
	// Even with the matching fingerprint, each cursor rank must have the right type.
	in.Query.Sort[1].Direction = "desc"
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(firstCursor, "q2."))
	var cursor queryCursor
	if e = json.Unmarshal(raw, &cursor); e != nil {
		t.Fatal(e)
	}
	cursor.Values[1] = json.RawMessage(`"wrong"`)
	raw, _ = json.Marshal(cursor)
	in.After = "q2." + base64.RawURLEncoding.EncodeToString(raw)
	if _, e = s.Query(testContext, "reader", l.ID, in); !isValidation(e) {
		t.Fatal("invalid secondary rank accepted", e)
	}
}

func TestMultiSortViewsAndSelectionValidation(t *testing.T) {
	s, _, l := fixture(t)
	field := fieldOK(t, s, l.ID, CreateField{Key: "n", Label: "N", Type: "decimal", Scale: 2, Indexed: true})
	fieldOK(t, s, l.ID, CreateField{Key: "note", Label: "Note", Type: "note"})
	for _, name := range []string{"B", "A"} {
		create(t, s, l.ID, "item", name, map[string]any{"n": "1.20", "note": "Text"})
	}
	spec := QuerySpec{Sort: []SortSpec{{Field: "n", Direction: "desc"}, {Field: "$name", Direction: "asc"}}}
	view, e := s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Sorted", Columns: []string{"n"}, Query: spec})
	if e != nil {
		t.Fatal(e)
	}
	out, e := s.QueryView(testContext, "alice", view.ID, ViewQueryRequest{})
	if e != nil || len(out.Data) != 2 || out.Data[0].Name != "A" || len(out.Data[0].Values) != 1 {
		t.Fatal(out, e)
	}
	out, e = s.QueryView(testContext, "alice", view.ID, ViewQueryRequest{Select: []string{"note"}})
	if e != nil || len(out.Data[0].Values) != 1 || out.Data[0].Values["note"] != "Text" {
		t.Fatal(out, e)
	}
	for _, in := range []QueryRequest{{Select: []string{"unknown"}}, {Select: []string{"n", "n"}}, {Query: QuerySpec{Sort: []SortSpec{{Field: "n"}, {Field: "n"}}}}, {Query: QuerySpec{Sort: []SortSpec{{Field: "$name"}, {Field: "note"}}}}, {Query: QuerySpec{Sort: []SortSpec{{Field: "$name"}, {Field: "n", Direction: "invalid"}}}}, {Query: QuerySpec{Sort: make([]SortSpec, 9)}}, {Select: make([]string, 51)}} {
		if _, e = s.Query(testContext, "alice", l.ID, in); !isValidation(e) {
			t.Fatal("invalid query accepted", in, e)
		}
	}
	for _, columns := range [][]string{{"unknown"}, {"n", "n"}} {
		if _, e = s.CreateView(testContext, "alice", l.ID, ViewInput{Name: "Invalid", Columns: columns}); !isValidation(e) {
			t.Fatal("invalid view columns accepted", columns, e)
		}
	}
	if e = s.DeleteField(testContext, "alice", l.ID, field.ID, latest(t, s, l.ID).Version); !isValidation(e) {
		t.Fatal("saved multi-key dependency removed", e)
	}
}
