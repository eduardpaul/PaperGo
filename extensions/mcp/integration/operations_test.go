package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/server"
	"papergo/internal/dms"
	"papergo/internal/worker"
)

// acme is a test contribution: one tool, available while open is set.
func acme(open *atomic.Bool) mcp.Contribution {
	return mcp.Contribution{ID: "acme.crm", Tools: []mcp.Tool{{
		Name: "acme_crm_whoami", Description: "Who the caller is.", Scopes: []string{"item.read"},
		InputSchema: json.RawMessage(`{"type": "object", "additionalProperties": false, "properties": {}}`),
		Annotations: mcp.Annotations{ReadOnly: true},
		Available:   func(context.Context, mcp.Caller) (bool, error) { return open.Load(), nil },
		Call: func(_ context.Context, c mcp.Caller, _ json.RawMessage) (json.RawMessage, error) {
			return json.Marshal(map[string]string{"subject": c.Subject()})
		},
	}}}
}

func TestContributions(t *testing.T) {
	var open atomic.Bool
	e := setup(t, options{contributions: []mcp.Contribution{acme(&open)}, enabled: []string{"acme.crm"}})
	s := e.connect(t, aliceToken)
	if slices.Contains(toolNames(t, s), "acme_crm_whoami") {
		t.Fatal("unavailable contribution listed")
	}
	unknown(t, s, "acme_crm_whoami")
	open.Store(true)
	if !slices.Contains(toolNames(t, s), "acme_crm_whoami") {
		t.Fatal("available contribution not listed")
	}
	if got := ok(t, s, "acme_crm_whoami", `{}`); got["subject"] != "alice" {
		t.Fatalf("whoami %v", got)
	}
	// Compiled but not enabled, it is not served at all.
	disabled := setup(t, options{contributions: []mcp.Contribution{acme(&open)}})
	ds := disabled.connect(t, aliceToken)
	if slices.Contains(toolNames(t, ds), "acme_crm_whoami") {
		t.Fatal("disabled contribution listed")
	}
	unknown(t, ds, "acme_crm_whoami")
}

// Assistants connect with a bearer credential. A proxy on loopback that
// forwards its public host reaches the endpoint like any other client.
func TestBearerAndHost(t *testing.T) {
	e := setup(t, options{})
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if r := post(t, e, list, map[string]string{"MCP-Protocol-Version": "2025-06-18"}); r.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(r.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("no credential: %d %v", r.StatusCode, r.Header)
	}
	if r := post(t, e, list, map[string]string{"Authorization": "Bearer pgo_revoked", "MCP-Protocol-Version": "2025-06-18"}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unknown token: %d", r.StatusCode)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, e.endpoint(), strings.NewReader(list))
	req.Host = "papergo.example"
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("public Host on loopback: %d", res.StatusCode)
	}
}

func TestOverloadAndLimits(t *testing.T) {
	e := setup(t, options{maxInFlight: 1, configure: func(c *server.Config) {
		c.MaxRequestBytes, c.MaxResultBytes = 4096, 2048
	}})
	f := seed(t, e)
	s := e.connect(t, aliceToken)

	// One call holds the API's only slot; the next request is refused.
	gate, arrived := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.mu.Lock()
	e.gate = gate
	e.before = func(r *http.Request) {
		if r.URL.Path == "/v1/workspaces" {
			once.Do(func() { close(arrived) })
		}
	}
	e.mu.Unlock()
	held := make(chan error, 1)
	go func() {
		_, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_workspaces", Arguments: map[string]any{}})
		held <- err
	}()
	<-arrived
	busy := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + aliceToken})
	e.mu.Lock()
	e.gate, e.before = nil, nil
	e.mu.Unlock()
	close(gate)
	if err := <-held; err != nil {
		t.Fatalf("held call: %v", err)
	}
	if busy.StatusCode != http.StatusServiceUnavailable || busy.Header.Get("Retry-After") == "" {
		t.Fatalf("overload: %d %v", busy.StatusCode, busy.Header)
	}
	if !strings.Contains(e.log(), `"status":503`) {
		t.Fatal("overload not logged")
	}

	// Oversized requests and answers fail explicitly.
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_item","arguments":{"item_id":"` + strings.Repeat("a", 5000) + `"}}}`
	if r := post(t, e, big, map[string]string{"Authorization": "Bearer " + aliceToken, "MCP-Protocol-Version": "2025-06-18"}); r.StatusCode != http.StatusRequestEntityTooLarge && r.StatusCode != http.StatusBadRequest {
		t.Fatalf("large request: %d", r.StatusCode)
	}
	fails(t, s, "query_items", `{"collection_id": "`+f.list+`"}`, "too_large")
}

// An answer that is not JSON is the API being unavailable, not data.
func TestInvalidAnswer(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	e.intercept(func(api http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/identity/me" {
				api.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `<html>`)
		})
	})
	failure := fails(t, s, "get_item", `{"item_id": "`+f.items[0]+`"}`, "unavailable")
	if !failure.Retryable {
		t.Fatalf("%+v", failure)
	}
}

func TestSchemaChangeDuringRead(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	var once sync.Once
	e.before = func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/content-types") {
			once.Do(func() {
				if _, err := e.svc.CreateField(context.Background(), "alice", f.list, dms.CreateField{Key: "late", Label: "Late", Type: "text"}); err != nil {
					t.Error(err)
				}
			})
		}
	}
	failure := fails(t, s, "get_collection_schema", `{"collection_id": "`+f.list+`"}`, "schema_changed")
	if !failure.Retryable {
		t.Fatalf("%+v", failure)
	}
	if schema := ok(t, s, "get_collection_schema", `{"collection_id": "`+f.list+`"}`); !strings.Contains(schema["$text"].(string), `"key":"late"`) {
		t.Fatalf("schema %s", schema["$text"])
	}
}

func TestSearch(t *testing.T) {
	e := setup(t, options{search: true})
	f := seed(t, e)
	other := create(t, e.svc, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Bob's"})
	bobsList := create(t, e.svc, "bob", other, dms.CreateResource{Kind: "list", Name: "Private"})
	create(t, e.svc, "bob", bobsList, dms.CreateResource{Kind: "item", Name: "Invoice secret"})
	if err := e.search.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Drain(t.Context(), e.svc); err != nil {
		t.Fatal(err)
	}
	s := e.connect(t, aliceToken)
	if !slices.Contains(toolNames(t, s), "search") {
		t.Fatal("search not listed")
	}
	var names []string
	args := `{"query": "invoice", "mode": "keyword", "limit": 2, "collection_id": "` + f.list + `"}`
	for range 5 {
		page := ok(t, s, "search", args)
		if page["mode"] != "keyword" {
			t.Fatalf("mode %v", page["mode"])
		}
		for _, hit := range page["value"].([]any) {
			h := hit.(map[string]any)
			if h["collection_id"] != f.list || h["workspace_id"] != f.workspace || h["collection_name"] != "Invoices" || h["workspace_name"] != "Operations" || h["highlights"] != nil {
				t.Fatalf("hit %v", h)
			}
			names = append(names, str(h["name"]))
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		args = `{"query": "invoice", "mode": "keyword", "limit": 2, "collection_id": "` + f.list + `", "cursor": "` + next + `"}`
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "Invoice 0,Invoice 1,Invoice 2,Invoice 3,Invoice 4" {
		t.Fatalf("hits %v", names)
	}
	everywhere := ok(t, s, "search", `{"query": "invoice"}`)
	if strings.Contains(everywhere["$text"].(string), "secret") {
		t.Fatal("another user's item leaked")
	}
	fails(t, s, "search", `{"query": "   "}`, "invalid_arguments")
	fails(t, s, "search", `{"query": "x", "limit": 51}`, "invalid_arguments")
	fails(t, s, "search", `{"query": "x", "mode": "fuzzy"}`, "invalid_arguments")
}

// A write whose answer fails is not sent again: the caller decides, with
// its own id, whether to repeat it.
func TestWritesAreNotRetried(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	var posts atomic.Int32
	e.intercept(func(api http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				api.ServeHTTP(w, r)
				return
			}
			posts.Add(1)
			api.ServeHTTP(httptest.NewRecorder(), r)
			// The item exists, but its answer is a failure.
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	})
	const id = "2f1e7c55-3b0e-4d8f-9b3e-5d7c1a2b3c4d"
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "id": "`+id+`", "name": "Lost answer"}`, "unavailable")
	if n := posts.Load(); n != 1 {
		t.Fatalf("%d writes sent", n)
	}
	e.intercept(nil)
	if again := ok(t, s, "create_item", `{"parent_id": "`+f.list+`", "id": "`+id+`", "name": "Lost answer"}`); again["created"] != false {
		t.Fatalf("repeat %v", again)
	}
}

// A write that succeeded is reported as written even when naming what it
// holds fails afterwards: a client must never repeat it as if it failed.
func TestWriteSurvivesLabelFailure(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	var dropped atomic.Int32
	e.intercept(func(api http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/fields") {
				dropped.Add(1)
				_, _ = io.WriteString(w, `<html>`)
				return
			}
			api.ServeHTTP(w, r)
		})
	})
	created := ok(t, s, "create_item", `{"parent_id": "`+f.list+`", "name": "Written", "values": {"status": "Open"}}`)
	if dropped.Load() == 0 || created["created"] != true || str(created["id"]) == "" || created["labels_missing"] != true {
		t.Fatalf("create %v", created)
	}
	updated := ok(t, s, "update_item", `{"item_id": "`+str(created["id"])+`", "version": `+fmt.Sprint(created["version"])+`, "name": "Rewritten"}`)
	if updated["name"] != "Rewritten" || updated["labels_missing"] != true {
		t.Fatalf("update %v", updated)
	}
	e.intercept(nil)
	if again := ok(t, s, "get_item", `{"item_id": "`+str(created["id"])+`"}`); again["labels_missing"] != nil {
		t.Fatalf("read %v", again)
	}
}
