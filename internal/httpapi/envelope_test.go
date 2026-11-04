package httpapi

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"papergo/internal/dms"
)

// A compact query returns each item's identity, version and values only;
// without an envelope, items keep their full metadata.
func TestCompactQueryEnvelope(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Temperatures"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "outdoor", Label: "Outdoor", Type: "decimal", Scale: 1, Indexed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "note", Label: "Note", Type: "text"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"-1.5", "6.0"} {
		if _, err = s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Temperature", Values: map[string]any{"outdoor": json.Number(v), "note": "shade"}}); err != nil {
			t.Fatal(err)
		}
	}
	page := func(path, body string) (int, []map[string]json.RawMessage) {
		t.Helper()
		w := request(h, "POST", path, body, testToken, "", "application/json")
		var out struct {
			Value []map[string]json.RawMessage `json:"value"`
		}
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, out.Value
	}
	keys := func(item map[string]json.RawMessage) []string {
		out := []string{}
		for k := range item {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}

	code, full := page("/v1/resources/"+list.ID+"/query", `{"query":{"sort":[{"field":"outdoor"}]}}`)
	if code != 200 || len(full) != 2 || full[0]["head_revision_id"] == nil || full[0]["workspace_id"] == nil {
		t.Fatalf("a query keeps its full items by default: %d %v", code, full)
	}
	code, compact := page("/v1/resources/"+list.ID+"/query", `{"envelope":"compact","select":["outdoor"],"query":{"sort":[{"field":"outdoor"}]}}`)
	if code != 200 || len(compact) != 2 || !slices.Equal(keys(compact[0]), []string{"content_type_id", "id", "kind", "name", "parent_id", "values", "version", "workspace_id"}) {
		t.Fatalf("compact items: %d %v", code, compact)
	}
	if string(compact[0]["values"]) != `{"outdoor":"-1.5"}` || string(compact[0]["id"]) != string(full[0]["id"]) || string(compact[0]["version"]) != string(full[0]["version"]) {
		t.Fatalf("a compact item's identity and selected values: %v", compact[0])
	}
	if _, all := page("/v1/resources/"+list.ID+"/query", `{"envelope":"compact"}`); len(all) != 2 || string(all[0]["values"]) == `{}` {
		t.Fatalf("compact items without select hold every value: %v", all)
	}
	if code, _ = page("/v1/resources/"+list.ID+"/query", `{"envelope":"tiny"}`); code != 422 {
		t.Fatalf("an unknown envelope: %d", code)
	}

	// What a query asks for stays: selected built-in columns, and whether an
	// item is archived.
	code, stamped := page("/v1/resources/"+list.ID+"/query", `{"envelope":"compact","select":["$created_at","$created_by","$modified_at","$modified_by"]}`)
	if code != 200 || len(stamped) != 2 || stamped[0]["created_at"] == nil || stamped[0]["created_by"] == nil || stamped[0]["updated_at"] == nil || stamped[0]["updated_by"] == nil || string(stamped[0]["created_at"]) != string(full[0]["created_at"]) && string(stamped[0]["created_at"]) != string(full[1]["created_at"]) {
		t.Fatalf("a compact query's selected built-in columns: %d %v", code, stamped)
	}
	var first string
	if err = json.Unmarshal(full[0]["id"], &first); err != nil {
		t.Fatal(err)
	}
	item, err := s.Get(ctx, "alice", first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetArchived(ctx, "alice", first, item.Version, true); err != nil {
		t.Fatal(err)
	}
	if code, rows := page("/v1/resources/"+list.ID+"/query", `{"envelope":"compact","query":{"archive":"all","sort":[{"field":"outdoor"}]}}`); code != 200 || len(rows) != 2 || string(rows[0]["archived"]) != "true" || rows[0]["archived_at"] == nil || rows[0]["archived_by"] == nil || rows[1]["archived"] != nil {
		t.Fatalf("a compact item says it is archived: %d %v", code, rows)
	}
	views, err := s.Views(ctx, "alice", list.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, rows := page("/v1/views/"+views.Data[0].ID+"/query", `{"envelope":"compact"}`); code != 200 || len(rows) != 1 || rows[0]["archived_at"] == nil || rows[0]["archived_by"] == nil {
		t.Fatalf("the Archived view keeps its columns: %d %v", code, rows)
	}
	item, err = s.Get(ctx, "alice", first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetArchived(ctx, "alice", first, item.Version, false); err != nil {
		t.Fatal(err)
	}

	view, err := s.CreateView(ctx, "alice", list.ID, dms.ViewInput{Name: "Cold first", Layout: "table", Columns: []string{"$name", "outdoor"}, Query: dms.QuerySpec{Sort: []dms.SortSpec{{Field: "outdoor"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if code, rows := page("/v1/views/"+view.ID+"/query", `{"envelope":"compact"}`); code != 200 || len(rows) != 2 || rows[0]["head_revision_id"] != nil || string(rows[0]["values"]) != `{"outdoor":"-1.5"}` {
		t.Fatalf("a compact view query: %d %v", code, rows)
	}
}
