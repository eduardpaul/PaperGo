package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"papergo/extensions/mcp/server"
	"papergo/internal/dal/blobs"
	"papergo/internal/dms"
)

// tasks is a list with publishing whose fields refer to terms, people and
// another collection's items, as an assistant meets them.
type tasks struct {
	fixture
	list, acme, europe string
}

func seedTasks(t *testing.T, e *env) tasks {
	t.Helper()
	k := tasks{fixture: seed(t, e)}
	k.list = str(rest(t, e, "POST", "/v1/resources/"+k.workspace+"/children", `{"kind": "list", "name": "Tasks", "publishing_enabled": true}`, "")["id"])
	group := str(rest(t, e, "POST", "/v1/term-groups", `{"name": "Customers"}`, "")["id"])
	set := str(rest(t, e, "POST", "/v1/term-sets", `{"group_id": "`+group+`", "key": "clients", "name": "Clients"}`, "")["id"])
	k.acme = str(rest(t, e, "POST", "/v1/term-sets/"+set+"/terms", `{"name": "Acme", "synonyms": ["ACME Corp"]}`, "")["id"])
	k.europe = str(rest(t, e, "POST", "/v1/term-sets/"+set+"/terms", `{"name": "Acme Europe", "parent_id": "`+k.acme+`"}`, "")["id"])
	for _, field := range []string{
		`{"key": "client", "label": "Client", "type": "term", "indexed": true, "options": {"term_set_id": "` + set + `"}}`,
		`{"key": "owner", "label": "Owner", "type": "person"}`,
		`{"key": "invoice", "label": "Invoice", "type": "lookup", "options": {"lookup_container_id": "` + k.fixture.list + `"}}`,
		`{"key": "note", "label": "Note", "type": "text"}`,
	} {
		rest(t, e, "POST", "/v1/resources/"+k.list+"/fields", field, "")
	}
	return k
}

// Entries name what their IDs refer to, link to the web app and say whether
// they are drafts, so an assistant can answer without more calls.
func TestEntriesNameLinkAndFlag(t *testing.T) {
	app, _ := url.Parse("https://papergo.example")
	e := setup(t, options{app: app})
	k := seedTasks(t, e)
	s := e.connect(t, aliceToken)

	created := ok(t, s, "create_item", `{"parent_id": "`+k.list+`", "name": "Call Acme", "keywords": ["urgent"],
		"values": {"client": "Acme", "owner": "alice", "invoice": "`+k.items[0]+`", "note": "about the late invoice"}}`)
	id := str(created["id"])
	labels := created["labels"].(map[string]any)
	keyword := created["keywords"].([]any)[0].(string)
	if labels[k.acme] != "Acme" || labels[k.items[0]] != "Invoice 0" || labels[keyword] != "urgent" || labels["alice"] == nil {
		t.Fatalf("labels %v", labels)
	}
	if created["draft"] != true || created["archived"] != false ||
		created["url"] != "https://papergo.example/w/"+k.workspace+"/l/"+k.list+"?item="+id {
		t.Fatalf("created %v", created)
	}
	got := ok(t, s, "get_item", `{"item_id": "`+id+`"}`)
	if got["labels"].(map[string]any)[k.acme] != "Acme" || got["draft"] != true {
		t.Fatalf("get_item %v", got)
	}
	// Readers of published content see no draft.
	if got := ok(t, s, "get_item", `{"item_id": "`+k.items[0]+`"}`); got["draft"] != false {
		t.Fatalf("published %v", got)
	}
	page := ok(t, s, "query_items", `{"collection_id": "`+k.list+`", "query": {"filter": {"field": "client", "op": "under", "value": "`+k.acme+`"}}}`)
	entries := page["value"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["labels"].(map[string]any)[k.items[0]] != "Invoice 0" {
		t.Fatalf("query %v", page)
	}
	folder := ok(t, s, "get_item", `{"item_id": "`+k.folder+`"}`)
	inFolder := ok(t, s, "create_item", `{"parent_id": "`+k.folder+`", "name": "Filed"}`)
	if folder["url"] != "https://papergo.example/w/"+k.workspace+"/l/"+k.fixture.list+"?folder="+k.folder ||
		inFolder["url"] != "https://papergo.example/w/"+k.workspace+"/l/"+k.fixture.list+"?folder="+k.folder+"&item="+str(inFolder["id"]) {
		t.Fatalf("folder links %v %v", folder["url"], inFolder["url"])
	}
	collections := ok(t, s, "list_collections", `{"workspace_id": "`+k.workspace+`"}`)
	if c := collections["value"].([]any)[0].(map[string]any); !strings.HasPrefix(str(c["url"]), "https://papergo.example/w/"+k.workspace+"/l/") {
		t.Fatalf("collection %v", c)
	}

	// A token without taxonomy or people scopes still reads; names it may
	// not read are left out.
	narrow := e.connect(t, e.token(t, "alice", "list.read").Secret)
	plain := ok(t, narrow, "get_item", `{"item_id": "`+id+`"}`)
	if labels := plain["labels"].(map[string]any); labels[k.items[0]] != "Invoice 0" || labels[k.acme] != nil || labels["alice"] != nil {
		t.Fatalf("narrow labels %v", labels)
	}

	// A filter on a field that is not indexed says which fields can be used.
	f := fails(t, s, "query_items", `{"collection_id": "`+k.list+`", "query": {"filter": {"field": "note", "op": "eq", "value": "x"}}}`, "invalid_arguments")
	if !strings.Contains(f.Message, "Fields that filters and sorts can use: client") {
		t.Fatalf("failure %+v", f)
	}
}

// Saved views run as people see them; navigation views and other
// collections' views are refused.
func TestViews(t *testing.T) {
	e := setup(t, options{})
	k := seedTasks(t, e)
	s := e.connect(t, aliceToken)
	ok(t, s, "create_item", `{"parent_id": "`+k.list+`", "name": "For Acme", "values": {"client": "`+k.europe+`"}}`)
	ok(t, s, "create_item", `{"parent_id": "`+k.list+`", "name": "Internal"}`)
	view := str(rest(t, e, "POST", "/v1/resources/"+k.list+"/views", `{"name": "Acme work", "query": {"filter": {"field": "client", "op": "under", "value": "`+k.acme+`"}}}`, "")["id"])
	browse := str(rest(t, e, "POST", "/v1/resources/"+k.list+"/views", `{"name": "Browse", "layout": "navigation", "settings": {"fields": [{"field": "client"}]}}`, "")["id"])
	other := str(rest(t, e, "POST", "/v1/resources/"+k.fixture.list+"/views", `{"name": "Open"}`, "")["id"])

	schema := ok(t, s, "get_collection_schema", `{"collection_id": "`+k.list+`"}`)
	var names []string
	for _, v := range schema["views"].([]any) {
		names = append(names, str(v.(map[string]any)["name"]))
	}
	if !strings.Contains(strings.Join(names, ","), "Acme work") {
		t.Fatalf("views %v", names)
	}
	page := ok(t, s, "query_items", `{"collection_id": "`+k.list+`", "view_id": "`+view+`"}`)
	if entries := page["value"].([]any); len(entries) != 1 || entries[0].(map[string]any)["name"] != "For Acme" {
		t.Fatalf("view %v", page)
	}
	fails(t, s, "query_items", `{"collection_id": "`+k.list+`", "view_id": "`+view+`", "query": {}}`, "invalid_arguments")
	fails(t, s, "query_items", `{"collection_id": "`+k.list+`", "view_id": "`+other+`"}`, "invalid_arguments")
	if f := fails(t, s, "query_items", `{"collection_id": "`+k.list+`", "view_id": "`+browse+`"}`, "invalid_arguments"); !strings.Contains(f.Message, "under") {
		t.Fatalf("navigation %+v", f)
	}
}

// Terms are listed for a field, its children and by search, so an
// assistant uses existing terms instead of guessing labels.
func TestListTerms(t *testing.T) {
	e := setup(t, options{})
	k := seedTasks(t, e)
	s := e.connect(t, aliceToken)
	ok(t, s, "create_item", `{"parent_id": "`+k.list+`", "name": "Tagged", "keywords": ["urgent"]}`)
	names := func(page map[string]any) string {
		var out []string
		for _, v := range page["value"].([]any) {
			out = append(out, str(v.(map[string]any)["name"]))
		}
		return strings.Join(out, ",")
	}
	roots := ok(t, s, "list_terms", `{"collection_id": "`+k.list+`", "field_key": "client"}`)
	if names(roots) != "Acme" || roots["value"].([]any)[0].(map[string]any)["has_children"] != true {
		t.Fatalf("roots %v", roots)
	}
	if got := names(ok(t, s, "list_terms", `{"collection_id": "`+k.list+`", "field_key": "client", "parent_id": "`+k.acme+`"}`)); got != "Acme Europe" {
		t.Fatalf("children %s", got)
	}
	if got := names(ok(t, s, "list_terms", `{"term_set_id": "`+str(roots["term_set_id"])+`", "search": "acme corp"}`)); got != "Acme" {
		t.Fatalf("synonym search %s", got)
	}
	if got := names(ok(t, s, "list_terms", `{"collection_id": "`+k.list+`", "field_key": "$keywords"}`)); !strings.Contains(got, "urgent") {
		t.Fatalf("keywords %s", got)
	}
	fails(t, s, "list_terms", `{"collection_id": "`+k.list+`", "field_key": "note"}`, "invalid_arguments")
	fails(t, s, "list_terms", `{"collection_id": "`+k.list+`"}`, "invalid_arguments")
	fails(t, s, "list_terms", `{"collection_id": "`+k.list+`", "field_key": "client", "term_set_id": "`+str(roots["term_set_id"])+`"}`, "invalid_arguments")
	if names := toolNames(t, e.connect(t, e.token(t, "alice", "list.read").Secret)); strings.Contains(strings.Join(names, ","), "list_terms") {
		t.Fatalf("list_terms without taxonomy.read: %v", names)
	}
}

// Archived documents are left out until asked for, and a document's text is
// read page by page within a character budget.
func TestArchiveAndText(t *testing.T) {
	e := setup(t, options{})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	doc := create(t, e.svc, "alice", f.library, dms.CreateResource{Kind: "item", Name: "Lease"})
	old := create(t, e.svc, "alice", f.library, dms.CreateResource{Kind: "item", Name: "Old lease"})
	rest(t, e, "POST", "/v1/items/"+old+"/archive", "", "1")
	if page := ok(t, s, "query_items", `{"collection_id": "`+f.library+`"}`); len(page["value"].([]any)) != 1 {
		t.Fatalf("active %v", page)
	}
	all := ok(t, s, "query_items", `{"collection_id": "`+f.library+`", "query": {"archive": "archived"}}`)
	if entries := all["value"].([]any); len(entries) != 1 || entries[0].(map[string]any)["archived"] != true {
		t.Fatalf("archived %v", all)
	}

	req, _ := http.NewRequestWithContext(t.Context(), "PUT", e.api.URL+"/v1/items/"+doc+"/content", strings.NewReader("%PDF-lease"))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/pdf")
	req.Header.Set("X-Filename", "lease.pdf")
	req.Header.Set("If-Match", `"1"`)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(res.Body).Decode(&blob); err != nil || res.StatusCode != 201 {
		t.Fatal(res.StatusCode, err)
	}
	res.Body.Close()
	if f := fails(t, s, "get_item_text", `{"item_id": "`+doc+`"}`, "not_found"); !strings.Contains(f.Message, "not been read") {
		t.Fatalf("unread %+v", f)
	}
	pages := []map[string]any{}
	for i, n := range []int{800, 800, 800, 1500} {
		pages = append(pages, map[string]any{"page": i + 1, "text": strings.Repeat(fmt.Sprint(i+1), n)})
	}
	stored, _ := json.Marshal(map[string]any{"pages": pages})
	saveText(t, e, blob.Sha256, stored)

	read := func(args string) (string, any) {
		out := ok(t, s, "get_item_text", `{"item_id": "`+doc+`", `+args+`}`)
		var got []string
		for _, p := range out["pages"].([]any) {
			p := p.(map[string]any)
			got = append(got, fmt.Sprintf("%v:%d:%v", p["page"], len(str(p["text"])), p["partial"] == true))
		}
		return strings.Join(got, " "), fmt.Sprintf("%v+%v", out["next_page"], out["next_offset"])
	}
	// Reads fill max_chars and resume where they stopped, inside a page too.
	for _, c := range []struct{ args, want, next string }{
		{`"max_chars": 1000`, "1:800:false 2:200:true", "2+200"},
		{`"page": 2, "offset": 200, "max_chars": 1600`, "2:600:true 3:800:false 4:200:true", "4+200"},
		{`"page": 4, "offset": 200, "max_chars": 1000`, "4:1000:true", "4+1200"},
		{`"page": 4, "offset": 1200, "max_chars": 1000`, "4:300:true", "<nil>+0"},
	} {
		if got, next := read(c.args); got != c.want || next != c.next {
			t.Fatalf("%s: %s next %s, want %s next %s", c.args, got, next, c.want, c.next)
		}
	}
	fails(t, s, "get_item_text", `{"item_id": "`+doc+`", "max_chars": 10}`, "invalid_arguments")
}

// saveText stores a file's page texts, as document.read_text does.
func saveText(t *testing.T, e *env, sha string, pages []byte) {
	t.Helper()
	obj, err := e.files.Put(context.Background(), bytes.NewReader(pages), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DAL.Blobs().SaveText(context.Background(), blobs.TextInput{SHA256: sha, Settings: "test", ObjectKey: obj.Key, ObjectSize: obj.Size,
		ObjectSHA256: obj.SHA256, Pages: 4, Chars: 3900, Language: "english", Extractor: "test"}); err != nil {
		t.Fatal(err)
	}
}

// A document whose whole text exceeds the server's answer limit is still
// read in parts: PaperGo cuts the range, so only the part asked for
// travels.
func TestLargeTextIsReadInParts(t *testing.T) {
	e := setup(t, options{configure: func(c *server.Config) { c.MaxBackendBytes = 64 << 10 }})
	f := seed(t, e)
	s := e.connect(t, aliceToken)
	doc := create(t, e.svc, "alice", f.library, dms.CreateResource{Kind: "item", Name: "Archive scan"})
	req, _ := http.NewRequestWithContext(t.Context(), "PUT", e.api.URL+"/v1/items/"+doc+"/content", strings.NewReader("%PDF-scan"))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/pdf")
	req.Header.Set("X-Filename", "scan.pdf")
	req.Header.Set("If-Match", `"1"`)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var blob struct {
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(res.Body).Decode(&blob); err != nil || res.StatusCode != 201 {
		t.Fatal(res.StatusCode, err)
	}
	res.Body.Close()
	stored, _ := json.Marshal(map[string]any{"pages": []map[string]any{{"page": 1, "text": strings.Repeat("a", 200<<10)}}})
	saveText(t, e, blob.Sha256, stored)
	out := ok(t, s, "get_item_text", `{"item_id": "`+doc+`", "max_chars": 1000}`)
	if page := out["pages"].([]any)[0].(map[string]any); len(str(page["text"])) != 1000 || fmt.Sprint(out["next_offset"]) != "1000" {
		t.Fatalf("part %v next %v+%v", len(str(page["text"])), out["next_page"], out["next_offset"])
	}
}
