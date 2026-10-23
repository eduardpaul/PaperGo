package integration

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"papergo/internal/identity"
	"papergo/internal/worker"
)

// A connection limited to one list sees that list alone, through every tool:
// discovery, reads, labels and search. Its token, not papergo-mcp, carries
// the limit, so the API enforces it for every request.
func TestLimitedConnection(t *testing.T) {
	e := setup(t, options{search: true})
	k := seedTasks(t, e)
	if err := e.search.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	s := e.connect(t, aliceToken)
	task := ok(t, s, "create_item", `{"parent_id": "`+k.list+`", "name": "Chase the invoice", "values": {"client": "Acme", "invoice": "`+k.items[0]+`"}}`)
	if err := worker.Drain(t.Context(), e.svc); err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.Directory().CreateAccessToken(t.Context(), "alice", identity.AccessTokenInput{Name: "Tasks assistant",
		Scopes: []string{"workspace.read", "item.read", "contentType.read", "taxonomy.read", "user.read"}, Resources: []string{k.list}})
	if err != nil {
		t.Fatal(err)
	}
	limited := e.connect(t, tok.Secret)
	names := func(page map[string]any) []string {
		var out []string
		for _, v := range page["value"].([]any) {
			out = append(out, str(v.(map[string]any)["name"]))
		}
		return out
	}
	if got := names(ok(t, limited, "list_workspaces", `{}`)); strings.Join(got, ",") != "Operations" {
		t.Fatalf("workspaces %v", got)
	}
	if got := names(ok(t, limited, "list_collections", `{"workspace_id": "`+k.workspace+`"}`)); strings.Join(got, ",") != "Tasks" {
		t.Fatalf("collections %v", got)
	}
	for _, args := range []string{`{"item_id": "` + k.items[0] + `"}`, `{"item_id": "` + k.folder + `"}`} {
		if _, f := call(t, limited, "get_item", args); f == nil || f.Code != "forbidden" && f.Code != "not_found" {
			t.Fatalf("read outside the limit %s: %+v", args, f)
		}
	}
	if _, f := call(t, limited, "query_items", `{"collection_id": "`+k.fixture.list+`"}`); f == nil {
		t.Fatal("query outside the limit")
	}
	got := ok(t, limited, "get_item", `{"item_id": "`+str(task["id"])+`"}`)
	labels := got["labels"].(map[string]any)
	if labels[k.acme] != "Acme" || labels[k.items[0]] != nil {
		t.Fatalf("labels %v", labels)
	}
	tools := toolNames(t, limited)
	if slices.Contains(tools, "create_item") || slices.Contains(tools, "update_item") {
		t.Fatalf("read-only connection offers writes: %v", tools)
	}
	hits := ok(t, limited, "search", `{"query": "invoice"}`)
	if len(hits["value"].([]any)) == 0 {
		t.Fatal("no hits inside the limit")
	}
	for _, h := range hits["value"].([]any) {
		if h := h.(map[string]any); h["collection_id"] != k.list {
			t.Fatalf("hit outside the limit %v", h)
		}
	}
	// Totals count only what the connection reaches, so they reveal nothing
	// about the places outside it, even when a search names one.
	if fmt.Sprint(hits["total"]) != fmt.Sprint(len(hits["value"].([]any))) {
		t.Fatalf("total %v for %d hits", hits["total"], len(hits["value"].([]any)))
	}
	outside := ok(t, limited, "search", `{"query": "invoice", "collection_id": "`+k.fixture.list+`"}`)
	if len(outside["value"].([]any)) != 0 || fmt.Sprint(outside["total"]) != "0" {
		t.Fatalf("search outside the limit %v", outside)
	}
	// Without the limit, the same search finds the invoices.
	if everywhere := ok(t, s, "search", `{"query": "invoice"}`); len(everywhere["value"].([]any)) <= len(hits["value"].([]any)) {
		t.Fatalf("unlimited search %v", everywhere)
	}
}
