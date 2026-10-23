package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"papergo/extensions/mcp"
	"papergo/extensions/mcp/server"
	"papergo/internal/dms"
	"papergo/internal/worker"
)

// acme is a test contribution: one tool, available while open is set.
func acme(open *atomic.Bool) mcp.Contribution {
	return mcp.Contribution{ID: "acme.crm", Tools: []mcp.Tool{{
		Name: "acme_crm_whoami", Description: "Who the caller is.", Scopes: []string{"list.read"},
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

func TestOrigins(t *testing.T) {
	e := setup(t, options{configure: func(c *server.Config) { c.AllowedOrigins = []string{"https://app.example"} }})
	list := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	auth := "Bearer " + aliceToken
	for origin, want := range map[string]int{"https://app.example": 200, "https://evil.example": 403, "null": 403} {
		r := post(t, e, list, map[string]string{"Authorization": auth, "Origin": origin, "MCP-Protocol-Version": "2025-06-18"})
		if r.StatusCode != want {
			t.Errorf("%s: %d, want %d", origin, r.StatusCode, want)
		}
	}
	// A browser's cross-site request without an Origin is refused too.
	if r := post(t, e, list, map[string]string{"Authorization": auth, "Sec-Fetch-Site": "cross-site"}); r.StatusCode != 403 {
		t.Errorf("cross-site: %d", r.StatusCode)
	}
	// The SDK's DNS-rebinding protection refuses a foreign Host on loopback.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, e.endpoint(), strings.NewReader(list))
	req.Host = "attacker.example"
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Errorf("foreign Host: %d", res.StatusCode)
	}
}

func TestOverloadAndLimits(t *testing.T) {
	e := setup(t, options{configure: func(c *server.Config) {
		c.MaxInFlight, c.AdmissionWait, c.MaxRequestBytes, c.MaxBackendBytes = 1, 50*time.Millisecond, 4096, 2048
	}})
	f := seed(t, e)
	s := e.connect(t, aliceToken)

	// One call holds the only slot at the API; the next request is refused.
	gate, arrived := make(chan struct{}), make(chan struct{})
	var once sync.Once
	e.gateMu.Lock()
	e.gate = gate
	e.before = func(r *http.Request) {
		if r.URL.Path == "/v1/workspaces" {
			once.Do(func() { close(arrived) })
		}
	}
	e.gateMu.Unlock()
	held := make(chan error, 1)
	go func() {
		_, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_workspaces", Arguments: map[string]any{}})
		held <- err
	}()
	<-arrived
	busy := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + aliceToken})
	e.gateMu.Lock()
	e.gate, e.before = nil, nil
	e.gateMu.Unlock()
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

func TestUnavailableBackend(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	// An answer that is not JSON is the API being unavailable, not data.
	e.backend = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/identity/me" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"subject":"alice","active":true,"scopes":["list.read"]}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `<html>`)
	})
	f2 := fails(t, s, "get_item", `{"item_id": "`+f.items[0]+`"}`, "unavailable")
	if !f2.Retryable {
		t.Fatalf("%+v", f2)
	}
	e.api.Close()
	if r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + aliceToken}); r.StatusCode != http.StatusServiceUnavailable || r.Header.Get("Retry-After") == "" {
		t.Fatalf("down: %d", r.StatusCode)
	}
	res, err := http.Get(e.mcp.URL + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready while the API is down: %d", res.StatusCode)
	}
	if res, err = http.Get(e.mcp.URL + "/health/live"); err != nil || res.StatusCode/100 != 2 {
		t.Fatalf("live: %v", err)
	}
	res.Body.Close()
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

// A write whose answer is lost is not sent again: the caller decides, with
// its own id, whether to repeat it.
func TestWritesAreNotRetried(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	api := e.backend
	var posts atomic.Int32
	e.backend = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			api.ServeHTTP(w, r)
			return
		}
		posts.Add(1)
		api.ServeHTTP(httptest.NewRecorder(), r)
		// The item exists, but its answer never arrives.
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			conn.Close()
		}
	})
	const id = "2f1e7c55-3b0e-4d8f-9b3e-5d7c1a2b3c4d"
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "id": "`+id+`", "name": "Lost answer"}`, "unavailable")
	if n := posts.Load(); n != 1 {
		t.Fatalf("%d writes sent", n)
	}
	e.backend = api
	if again := ok(t, s, "create_item", `{"parent_id": "`+f.list+`", "id": "`+id+`", "name": "Lost answer"}`); again["created"] != false {
		t.Fatalf("repeat %v", again)
	}
}
