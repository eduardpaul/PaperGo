package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/workflow"
)

func searchRegistered() bool {
	return slices.ContainsFunc(workflow.Roles(), func(r *workflow.Role) bool { return r.Key == "search.index" })
}

// fixture is alice's workspace with a list holding exact numbers and a
// library.
type fixture struct {
	workspace, list, library, folder string
	items                            []string
}

func seed(t *testing.T, e *env) fixture {
	t.Helper()
	ctx := t.Context()
	var f fixture
	f.workspace = create(t, e.svc, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Operations"})
	create(t, e.svc, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Archive"})
	f.list = create(t, e.svc, "alice", f.workspace, dms.CreateResource{Kind: "list", Name: "Invoices"})
	f.library = create(t, e.svc, "alice", f.workspace, dms.CreateResource{Kind: "library", Name: "Contracts"})
	for _, field := range []dms.CreateField{
		{Key: "count", Label: "Count", Type: "integer", Indexed: true},
		{Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true},
		{Key: "status", Label: "Status", Type: "choice", Choices: []string{"Open", "Paid"}, Indexed: true},
	} {
		if _, err := e.svc.CreateField(ctx, "alice", f.list, field); err != nil {
			t.Fatal(err)
		}
	}
	f.folder = create(t, e.svc, "alice", f.list, dms.CreateResource{Kind: "folder", Name: "2026"})
	for i := range 5 {
		status := "Open"
		if i%2 == 1 {
			status = "Paid"
		}
		f.items = append(f.items, create(t, e.svc, "alice", f.list, dms.CreateResource{Kind: "item", Name: fmt.Sprintf("Invoice %d", i),
			Values: map[string]any{"count": json.Number(fmt.Sprintf("900719925474099%d", i)), "amount": json.Number(fmt.Sprintf("1234567890123456.%d1", i)), "status": status}}))
	}
	return f
}

func TestToolsAndProtocol(t *testing.T) {
	e := setup(t, options{})
	s := e.connect(t, aliceToken)
	res, err := s.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		readOnly := tool.Annotations.ReadOnlyHint
		write := tool.Name == "create_item" || tool.Name == "update_item"
		// Updates overwrite and clear values; creates only add.
		if readOnly == write || *tool.Annotations.DestructiveHint != (tool.Name == "update_item") || *tool.Annotations.OpenWorldHint || tool.Annotations.IdempotentHint == write {
			t.Errorf("%s annotations %+v", tool.Name, tool.Annotations)
		}
		schema, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(schema), `"additionalProperties":false`) || tool.OutputSchema == nil || tool.Description == "" {
			t.Errorf("%s schema %s", tool.Name, schema)
		}
	}
	// Search is optional: without it the API has no search, and the tool is
	// neither listed nor callable. Search's workflow roles are registered
	// process-wide, so this holds until TestSearch registers them.
	want := "create_item,get_collection_schema,get_item,get_item_text,list_collections,list_terms,list_workspaces,query_items,update_item"
	if searchRegistered() {
		want = "create_item,get_collection_schema,get_item,get_item_text,list_collections,list_terms,list_workspaces,query_items,search,update_item"
	} else {
		unknown(t, s, "search")
	}
	if strings.Join(names, ",") != want {
		t.Fatalf("tools %v", names)
	}
	unknown(t, s, "delete_item")

	// Every negotiated protocol revision works without a session.
	if v := s.InitializeResult(); v != nil && v.ProtocolVersion != "2026-07-28" {
		t.Errorf("negotiated %s", v.ProtocolVersion)
	}
	for _, version := range []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"} {
		r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":{},"clientInfo":{"name":"raw","version":"1"}}}`,
			map[string]string{"Authorization": "Bearer " + aliceToken, "MCP-Protocol-Version": version})
		body, _ := io.ReadAll(r.Body)
		if r.StatusCode != 200 || !strings.Contains(string(body), `"protocolVersion":"`+version+`"`) || r.Header.Get("Mcp-Session-Id") != "" {
			t.Errorf("%s: %d %s %v", version, r.StatusCode, body, r.Header)
		}
		if r.Header.Get("Cache-Control") != "private, no-store" || r.Header.Get("X-Request-ID") == "" {
			t.Errorf("%s: headers %v", version, r.Header)
		}
	}
	r := post(t, e, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + aliceToken, "MCP-Protocol-Version": "2025-06-18"})
	if body, _ := io.ReadAll(r.Body); r.StatusCode != 200 || !strings.Contains(string(body), `"list_workspaces"`) {
		t.Fatalf("tools/list without a session: %d %s", r.StatusCode, body)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req, _ := http.NewRequestWithContext(t.Context(), method, e.endpoint(), nil)
		req.Header.Set("Authorization", "Bearer "+aliceToken)
		req.Header.Set("Accept", "text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", method, res.StatusCode)
		}
	}
	for _, path := range []string{"/health/live", "/health/ready"} {
		res, err := http.Get(e.mcp.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Errorf("%s: %d", path, res.StatusCode)
		}
	}
	if logs := e.log(); strings.Contains(logs, aliceToken) || !strings.Contains(logs, `"mcp_request"`) {
		t.Fatalf("logs %s", logs)
	}
}

func TestDiscoveryAndReads(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)

	// Workspaces page one at a time.
	var seen []string
	args := `{"limit": 1}`
	for range 3 {
		page := ok(t, s, "list_workspaces", args)
		for _, w := range page["value"].([]any) {
			seen = append(seen, str(w.(map[string]any)["name"]))
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		args = `{"limit": 1, "cursor": "` + next + `"}`
	}
	slices.Sort(seen)
	if strings.Join(seen, ",") != "Archive,Operations" {
		t.Fatalf("workspaces %v", seen)
	}
	fails(t, s, "list_workspaces", `{"limit": 2, "cursor": "`+strings.TrimPrefix(args, `{"limit": 1, "cursor": "`)[:10]+`"}`, "invalid_arguments")

	collections := ok(t, s, "list_collections", `{"workspace_id": "`+f.workspace+`"}`)
	var kinds []string
	for _, c := range collections["value"].([]any) {
		kinds = append(kinds, str(c.(map[string]any)["kind"])+":"+str(c.(map[string]any)["name"]))
	}
	slices.Sort(kinds)
	if strings.Join(kinds, ",") != "library:Contracts,list:Invoices" || collections["next_cursor"] != nil {
		t.Fatalf("collections %v", collections)
	}
	fails(t, s, "list_collections", `{"workspace_id": "`+f.list+`"}`, "invalid_arguments")
	fails(t, s, "list_collections", `{"workspace_id": "not-a-uuid"}`, "invalid_arguments")
	fails(t, s, "list_collections", `{"workspace_id": "00000000-0000-4000-8000-000000000000"}`, "not_found")

	schema := ok(t, s, "get_collection_schema", `{"collection_id": "`+f.list+`"}`)
	text := schema["$text"].(string)
	for _, want := range []string{`"key":"count"`, `"key":"amount"`, `"scale":2`, `"is_default":true`, `"kind":"list"`} {
		if !strings.Contains(text, want) {
			t.Errorf("schema lacks %s: %s", want, text)
		}
	}
	fails(t, s, "get_collection_schema", `{"collection_id": "`+f.items[0]+`"}`, "invalid_arguments")

	item := ok(t, s, "get_item", `{"item_id": "`+f.items[3]+`"}`)
	if item["collection_id"] != f.list || item["kind"] != "item" || !strings.Contains(item["$text"].(string), `"count":9007199254740993`) ||
		!strings.Contains(item["$text"].(string), `"access":["read","read_draft","write","publish","manage"]`) {
		t.Fatalf("item %s", item["$text"])
	}
	if strings.Contains(item["$text"].(string), "container_id") {
		t.Fatal("container_id leaked")
	}
	folder := ok(t, s, "get_item", `{"item_id": "`+f.folder+`", "surface": "head"}`)
	if folder["kind"] != "folder" {
		t.Fatalf("folder %v", folder)
	}
	fails(t, s, "get_item", `{"item_id": "`+f.list+`"}`, "invalid_arguments")
	fails(t, s, "get_item", `{"item_id": "`+f.items[0]+`", "surface": "draft"}`, "invalid_arguments")
	fails(t, s, "get_item", `{"item_id": "`+f.items[0]+`", "extra": 1}`, "invalid_arguments")
	fails(t, s, "get_item", `{"item_id": 7}`, "invalid_arguments")
}

func TestQueryItems(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)

	// A filter literal above 2^53 keeps every digit on the way in and out.
	exact := ok(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"filter": {"field": "count", "op": "eq", "value": 9007199254740993}}}`)
	if v := exact["value"].([]any); len(v) != 1 || v[0].(map[string]any)["id"] != f.items[3] {
		t.Fatalf("exact filter %s", exact["$text"])
	}
	decimal := ok(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"filter": {"field": "amount", "op": "gte", "value": 1234567890123456.31}}, "select": ["amount"]}`)
	if v := decimal["value"].([]any); len(v) != 2 || !strings.Contains(decimal["$text"].(string), "1234567890123456.41") || strings.Contains(decimal["$text"].(string), `"count"`) {
		t.Fatalf("decimal filter %s", decimal["$text"])
	}

	// Paging two at a time, newest count first, only open invoices.
	args := `{"collection_id": "` + f.list + `", "limit": 2, "query": {"filter": {"field": "status", "op": "eq", "value": "Open"}, "sort": [{"field": "count", "direction": "desc"}]}, "select": []}`
	var names []string
	cursor := ""
	for range 5 {
		call := args
		if cursor != "" {
			call = strings.TrimSuffix(args, "}") + `, "cursor": "` + cursor + `"}`
		}
		page := ok(t, s, "query_items", call)
		for _, it := range page["value"].([]any) {
			m := it.(map[string]any)
			names = append(names, str(m["name"]))
			if len(m["values"].(map[string]any)) != 0 {
				t.Fatalf("select [] returned values %v", m)
			}
		}
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
		// The cursor belongs to this query only.
		fails(t, s, "query_items", `{"collection_id": "`+f.list+`", "limit": 3, "cursor": "`+next+`"}`, "invalid_arguments")
		fails(t, s, "list_workspaces", `{"cursor": "`+next+`"}`, "invalid_arguments")
	}
	if strings.Join(names, ",") != "Invoice 4,Invoice 2,Invoice 0" {
		t.Fatalf("pages %v", names)
	}
	empty := ok(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"filter": {"field": "status", "op": "eq", "value": "Void"}}}`)
	if empty["$text"].(string) != `{"value":[],"next_cursor":null}` {
		t.Fatalf("empty %s", empty["$text"])
	}
	inFolder := ok(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"parent_id": "`+f.folder+`"}}`)
	if len(inFolder["value"].([]any)) != 0 {
		t.Fatalf("folder %s", inFolder["$text"])
	}
	named := ok(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"search": "Invoice 3"}}`)
	if v := named["value"].([]any); len(v) != 1 {
		t.Fatalf("name search %s", named["$text"])
	}
	fails(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"group_by": "status"}}`, "invalid_arguments")
	fails(t, s, "query_items", `{"collection_id": "`+f.list+`", "query": {"filter": {"field": "missing_field", "op": "eq", "value": 1}}}`, "invalid_arguments")
	fails(t, s, "query_items", `{"collection_id": "`+f.list+`", "limit": 201}`, "invalid_arguments")
	fails(t, s, "query_items", `{"collection_id": "`+f.list+`", "limit": "5"}`, "invalid_arguments")
	fails(t, s, "query_items", `{"collection_id": "`+f.workspace+`"}`, "invalid_arguments")
}

func TestWrites(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)

	const id = "7b0d1c1e-8a52-4c41-9f7e-0e7a2b9d6c11"
	created := ok(t, s, "create_item", `{"parent_id": "`+f.folder+`", "id": "`+id+`", "name": "Invoice X", "values": {"count": 9223372036854775807, "amount": "99999999999999.99", "status": "Open"}, "keywords": ["urgent"]}`)
	text := created["$text"].(string)
	if created["created"] != true || created["id"] != id || created["parent_id"] != f.folder || created["collection_id"] != f.list ||
		!strings.Contains(text, `"count":9223372036854775807`) || !strings.Contains(text, "99999999999999.99") {
		t.Fatalf("created %s", text)
	}
	// Repeating the call returns the item unchanged.
	again := ok(t, s, "create_item", `{"parent_id": "`+f.folder+`", "id": "`+id+`", "name": "Other name"}`)
	if again["created"] != false || again["name"] != "Invoice X" || again["version"] != created["version"] {
		t.Fatalf("repeat %s", again["$text"])
	}
	fails(t, s, "create_item", `{"parent_id": "`+f.workspace+`", "name": "Nowhere"}`, "invalid_arguments")
	fails(t, s, "create_item", `{"parent_id": "`+f.items[0]+`", "name": "Nested"}`, "invalid_arguments")
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "name": "Bad", "values": {"count": 1.5}}`, "invalid_arguments")
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "name": "Bad", "values": {"status": "Void"}}`, "invalid_arguments")
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "name": ""}`, "invalid_arguments")
	fails(t, s, "create_item", `{"parent_id": "`+f.list+`", "kind": "folder", "name": "x"}`, "invalid_arguments")

	version := created["version"].(json.Number).String()
	updated := ok(t, s, "update_item", `{"item_id": "`+id+`", "version": `+version+`, "values": {"status": "Paid", "amount": null}}`)
	if updated["version"] == created["version"] || !strings.Contains(updated["$text"].(string), `"status":"Paid"`) ||
		strings.Contains(updated["$text"].(string), `"amount"`) || !strings.Contains(updated["$text"].(string), `"count":9223372036854775807`) ||
		updated["access"] == nil {
		t.Fatalf("updated %s", updated["$text"])
	}
	// The old version is stale: nothing changes and the caller is told.
	stale := fails(t, s, "update_item", `{"item_id": "`+id+`", "version": `+version+`, "name": "Late"}`, "version_conflict")
	if stale.RequestID == "" {
		t.Fatalf("stale %+v", stale)
	}
	if got := ok(t, s, "get_item", `{"item_id": "`+id+`"}`); got["name"] != "Invoice X" {
		t.Fatalf("stale write applied %v", got)
	}
	fails(t, s, "update_item", `{"item_id": "`+id+`", "version": `+version+`}`, "invalid_arguments")
	fails(t, s, "update_item", `{"item_id": "`+id+`", "version": 0, "name": "x"}`, "invalid_arguments")
	fails(t, s, "update_item", `{"item_id": "`+f.list+`", "version": 1, "name": "x"}`, "invalid_arguments")
	renamed := ok(t, s, "update_item", `{"item_id": "`+id+`", "version": `+updated["version"].(json.Number).String()+`, "name": "Invoice Y", "keywords": []}`)
	if renamed["name"] != "Invoice Y" || len(renamed["keywords"].([]any)) != 0 {
		t.Fatalf("renamed %s", renamed["$text"])
	}
	// The write is alice's, as through the API.
	r, err := e.svc.Get(t.Context(), "alice", id)
	if err != nil || r.UpdatedBy != "alice" || r.Name != "Invoice Y" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestIdentitiesStayApart(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	bobsWorkspace := create(t, e.svc, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Bob's"})
	alice := e.connect(t, aliceToken)
	bob := e.connect(t, e.token(t, "bob", "workspace.read", "item.read", "item.write", "contentType.read").Secret)
	reader := e.token(t, "alice", "item.read")
	readOnly := e.connect(t, reader.Secret)

	done := make(chan []string, 2)
	for _, c := range []struct {
		s    *sdk.ClientSession
		want string
	}{{alice, "Archive,Operations"}, {bob, "Bob's"}} {
		go func() {
			var errs []string
			for range 20 {
				page, err := c.s.CallTool(t.Context(), &sdk.CallToolParams{Name: "list_workspaces", Arguments: map[string]any{}})
				if err != nil {
					errs = append(errs, err.Error())
					continue
				}
				var got struct{ Value []struct{ Name string } }
				raw, _ := json.Marshal(page.StructuredContent)
				_ = json.Unmarshal(raw, &got)
				var names []string
				for _, w := range got.Value {
					names = append(names, w.Name)
				}
				slices.Sort(names)
				if strings.Join(names, ",") != c.want {
					errs = append(errs, strings.Join(names, ","))
				}
			}
			done <- errs
		}()
	}
	for range 2 {
		if errs := <-done; len(errs) > 0 {
			t.Fatalf("identity leak or failure: %v", errs)
		}
	}
	if code := fails(t, bob, "get_item", `{"item_id": "`+f.items[0]+`"}`, "forbidden").Code; code == "" {
		t.Fatal()
	}
	ok(t, bob, "list_collections", `{"workspace_id": "`+bobsWorkspace+`"}`)

	// A token's scopes narrow the tools its holder sees and may call.
	// (Search, also item.read, is listed once TestSearch registered it.)
	names := slices.DeleteFunc(toolNames(t, readOnly), func(n string) bool { return n == "search" && searchRegistered() })
	if names := strings.Join(names, ","); names != "get_item,query_items" {
		t.Fatalf("read-only tools %s", names)
	}
	unknown(t, readOnly, "create_item")
	unknown(t, readOnly, "list_workspaces")
	ok(t, readOnly, "get_item", `{"item_id": "`+f.items[0]+`"}`)

	// Revoking the token ends its access at once.
	if err := e.svc.Directory().RevokeAccessToken(t.Context(), "alice", reader.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.CallTool(t.Context(), &sdk.CallToolParams{Name: "get_item", Arguments: map[string]any{"item_id": f.items[0]}}); err == nil {
		t.Fatal("revoked token still served")
	}
	r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + reader.Secret})
	if r.StatusCode != 401 || !strings.Contains(r.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("revoked: %d %v", r.StatusCode, r.Header)
	}
	for _, h := range []map[string]string{{}, {"Authorization": "Basic YTpi"}, {"Authorization": "Bearer "}, {"Authorization": "Bearer a b"}} {
		r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, h)
		if r.StatusCode != 401 || r.Header.Get("WWW-Authenticate") != `Bearer realm="PaperGo"` {
			t.Errorf("%v: %d %v", h, r.StatusCode, r.Header)
		}
	}
	if _, err := e.dial(t, "wrong"); err == nil {
		t.Fatal("invalid token connected")
	}
	// An inactive user is refused before any tool runs.
	carol := e.token(t, "carol", "workspace.read")
	if _, err := e.svc.Directory().SetUserActive(t.Context(), "organization-admin", "carol", false); err != nil {
		t.Fatal(err)
	}
	if r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + carol.Secret}); r.StatusCode != 401 && r.StatusCode != 403 {
		t.Fatalf("inactive user: %d", r.StatusCode)
	}
}

// The SDK client decodes structured content as float64; on the wire both
// the text block and the structured content keep every digit.
func TestNumbersStayExactOnTheWire(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	r := post(t, e, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"query_items","arguments":{"collection_id":"`+f.list+`","query":{"filter":{"field":"count","op":"in","value":[9007199254740993,9007199254740994]}},"select":["count","amount"]}}}`,
		map[string]string{"Authorization": "Bearer " + aliceToken, "MCP-Protocol-Version": "2025-06-18"})
	body, _ := io.ReadAll(r.Body)
	var res struct {
		Result struct {
			Content           []struct{ Text string } `json:"content"`
			StructuredContent json.RawMessage         `json:"structuredContent"`
		} `json:"result"`
	}
	if r.StatusCode != 200 || json.Unmarshal(body, &res) != nil || len(res.Result.Content) != 1 {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	for _, raw := range []string{string(res.Result.StructuredContent), res.Result.Content[0].Text} {
		for _, want := range []string{`"count":9007199254740993`, `"count":9007199254740994`, `"amount":"1234567890123456.31"`, `"amount":"1234567890123456.41"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("%s lacks %s", raw, want)
			}
		}
	}
}

// An integration token acts as its own service identity: it reads only what
// that identity was given.
func TestIntegrationTokens(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	ctx := t.Context()
	integration, err := e.svc.Directory().CreateIntegrationToken(ctx, "organization-admin", identity.AccessTokenInput{Name: "MCP service", Scopes: []string{"workspace.read", "item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	s := e.connect(t, integration.Secret)
	if page := ok(t, s, "list_workspaces", `{}`); len(page["value"].([]any)) != 0 {
		t.Fatalf("service sees %s", page["$text"])
	}
	fails(t, s, "get_item", `{"item_id": "`+f.items[0]+`"}`, "forbidden")
	if err := e.svc.SetWorkspaceMember(ctx, "alice", f.workspace, dms.WorkspaceMember{Subject: integration.Token.Subject, Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if got := ok(t, s, "get_item", `{"item_id": "`+f.items[0]+`"}`); !slices.Contains(got["access"].([]any), any("read")) || slices.Contains(got["access"].([]any), any("write")) {
		t.Fatalf("access %v", got["access"])
	}
	unknown(t, s, "create_item")
}
