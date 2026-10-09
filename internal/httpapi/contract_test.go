package httpapi

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestOpenAPIDocumentIsCurrent(t *testing.T) {
	spec, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(spec, committed) {
		t.Fatal("api/openapi.json is stale; run go generate ./internal/httpapi")
	}
}

// contract matches requests to documented operations.
var contract = sync.OnceValues(func() (*huma.OpenAPI, *http.ServeMux) {
	oapi := (&API{}).OpenAPI()
	mux := http.NewServeMux()
	for path, item := range oapi.Paths {
		for method, op := range map[string]*huma.Operation{"GET": item.Get, "POST": item.Post, "PUT": item.Put, "PATCH": item.Patch, "DELETE": item.Delete} {
			if op != nil {
				mux.HandleFunc(method+" "+path, func(http.ResponseWriter, *http.Request) {})
			}
		}
	}
	return oapi, mux
})

type recorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *recorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.body.Write(p)
	return w.ResponseWriter.Write(p)
}
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// conformant fails the test whenever h answers a documented operation with an
// undocumented status, a missing documented header or a body that violates
// the response schema, so every HTTP test also checks the generated contract.
func conformant(t *testing.T, h http.Handler) http.Handler {
	oapi, mux := contract()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		_, pattern := mux.Handler(r)
		method, path, ok := strings.Cut(pattern, " ")
		if !ok || rec.status == 0 {
			return
		}
		op := map[string]*huma.Operation{"GET": oapi.Paths[path].Get, "POST": oapi.Paths[path].Post, "PUT": oapi.Paths[path].Put, "PATCH": oapi.Paths[path].Patch, "DELETE": oapi.Paths[path].Delete}[method]
		if r.Method == http.MethodHead {
			return
		}
		res := op.Responses[strconv.Itoa(rec.status)]
		if res == nil {
			t.Errorf("%s: undocumented status %d: %s", pattern, rec.status, rec.body.String())
			return
		}
		if name, found := strings.CutPrefix(res.Ref, "#/components/responses/"); found {
			res = oapi.Components.Responses[name]
		}
		for name := range res.Headers {
			if w.Header().Get(name) == "" {
				t.Errorf("%s: %d response lacks documented header %s", pattern, rec.status, name)
			}
		}
		if len(res.Content) == 0 {
			if rec.body.Len() > 0 {
				t.Errorf("%s: %d response has an undocumented body", pattern, rec.status)
			}
			return
		}
		media, _, _ := mime.ParseMediaType(w.Header().Get("Content-Type"))
		content := res.Content[media]
		if content == nil {
			// Binary bodies carry their own media type.
			if res.Content["application/octet-stream"] == nil {
				t.Errorf("%s: %d response has undocumented content type %q", pattern, rec.status, media)
			}
			return
		}
		decoder := json.NewDecoder(&rec.body)
		decoder.UseNumber()
		var body any
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("%s: invalid JSON response: %v", pattern, err)
			return
		}
		result := &huma.ValidateResult{}
		huma.Validate(oapi.Components.Schemas, content.Schema, huma.NewPathBuffer([]byte{}, 0), huma.ModeReadFromServer, body, result)
		for _, err := range result.Errors {
			t.Errorf("%s: %d response violates the contract: %v", pattern, rec.status, err)
		}
	})
}

// TestRESTCatalogMatchesContract walks the operations that other tests do not
// call, so the conformance check covers every documented JSON operation.
func TestRESTCatalogMatchesContract(t *testing.T) {
	h, _, _ := setup(t)
	send := func(method, path, body, match string, expected int) map[string]any {
		t.Helper()
		media := ""
		if body != "" {
			media = "application/json"
		}
		res := request(h, method, path, body, testToken, match, media)
		if res.Code != expected {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		return out
	}
	id := func(v map[string]any) string { return v["id"].(string) }
	send("GET", "/health/ready", "", "", 200)
	ws := id(send("POST", "/v1/workspaces", `{"name":"Catalog","tags":["contract"]}`, "", 201))
	list := id(send("POST", "/v1/resources/"+ws+"/children", `{"kind":"list","name":"Tasks"}`, "", 201))
	send("POST", "/v1/resources/"+list+"/fields", `{"key":"status","label":"Status","type":"choice","choices":["open","done"],"indexed":true}`, "", 201)
	send("GET", "/v1/resources/"+list+"/fields", "", "", 200)
	a := id(send("POST", "/v1/resources/"+list+"/children", `{"kind":"item","name":"A","values":{"status":"open"}}`, "", 201))
	b := id(send("POST", "/v1/resources/"+list+"/children", `{"kind":"item","name":"B"}`, "", 201))
	send("GET", "/v1/resources?workspace_id="+ws, "", "", 200)
	send("GET", "/v1/resources?parent_id="+list+"&filter_field=status&filter_value=open", "", "", 200)
	send("GET", "/v1/resources?parent_id="+list+"&limit=0", "", "", 422)
	send("GET", "/v1/resources/"+ws+"/missing", "", "", 404)

	tpl := id(send("POST", "/v1/workspaces/"+ws+"/templates", `{"key":"record","name":"Record","fields":[{"key":"serial","label":"Serial","type":"integer"}]}`, "", 201))
	send("GET", "/v1/workspaces/"+ws+"/templates", "", "", 200)
	send("PUT", "/v1/templates/"+tpl, `{"key":"record","name":"Record v2","fields":[{"key":"serial","label":"Serial","type":"integer"}]}`, `"1"`, 200)

	set := id(send("POST", "/v1/workspaces/"+ws+"/term-sets", `{"key":"topics","name":"Topics"}`, "", 201))
	send("GET", "/v1/term-sets/"+set, "", "", 200)
	send("PUT", "/v1/term-sets/"+set, `{"key":"topics","name":"Topics v2"}`, `"1"`, 200)
	send("POST", "/v1/term-sets/"+set+"/terms", `{"name":"Finance","synonyms":["Money"]}`, "", 201)
	if terms := send("GET", "/v1/term-sets/"+set+"/terms?q=money", "", "", 200); len(terms["data"].([]any)) != 1 {
		t.Fatal("term search", terms)
	}

	typ := id(send("POST", "/v1/workspaces/"+ws+"/relationship-types", `{"key":"blocks","label":"Blocks","directed":true}`, "", 201))
	send("GET", "/v1/relationship-types/"+typ, "", "", 200)
	send("PUT", "/v1/relationship-types/"+typ, `{"key":"blocks","label":"Blocks work","inverse_label":"Blocked by","directed":true}`, `"1"`, 200)
	send("POST", "/v1/items/"+a+"/relationships", `{"type_id":"`+typ+`","target_id":"`+b+`"}`, "", 201)
	send("GET", "/v1/items/"+a+"/relationships", "", "", 200)
	if in := send("GET", "/v1/items/"+b+"/relationships?direction=incoming&name=blocks", "", "", 200); len(in["data"].([]any)) != 1 {
		t.Fatal("incoming relationships", in)
	}

	view := id(send("POST", "/v1/resources/"+list+"/views", `{"name":"By status","query":{"group_by":"status"}}`, "", 201))
	send("GET", "/v1/resources/"+list+"/views", "", "", 200)
	send("GET", "/v1/views/"+view, "", "", 200)
	send("POST", "/v1/views/"+view+"/query/groups", `{}`, "", 200)
	send("GET", "/v1/workspaces/"+ws+"/audit?limit=5", "", "", 200)
}
