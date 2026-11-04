package apps

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

const token = "0123456789abcdef0123456789abcdef"

func call(h http.Handler, method, path, body string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The apps routes create, read, replace and delete apps at their ETag, and
// serve the compiled code to the app page.
func TestAppRoutes(t *testing.T) {
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "organization-admin"}, Logger: slog.Default(), Modules: []httpapi.Module{Module()}}
	h := a.Handler()
	ws, err := s.Create(t.Context(), "organization-admin", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	body := func(source string) string {
		raw, _ := json.Marshal(map[string]any{"name": "Hello", "source": source, "state": "published"})
		return string(raw)
	}
	source := `import { app } from "@papergo/app";
export default app({ render: () => "Hello" });`

	w := call(h, "POST", "/v1/workspaces/"+ws.ID+"/apps", body("export default 1;"))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "does not compile") {
		t.Fatalf("a broken source: %d %s", w.Code, w.Body)
	}
	w = call(h, "POST", "/v1/workspaces/"+ws.ID+"/apps", body(source))
	var created View
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` || json.Unmarshal(w.Body.Bytes(), &created) != nil || w.Header().Get("Location") != "/v1/apps/"+created.ID || created.Key != "hello" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", "/v1/workspaces/"+ws.ID+"/apps", ""); w.Code != 200 || !strings.Contains(w.Body.String(), created.ID) || strings.Contains(w.Body.String(), `"source"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", "/v1/apps/"+created.ID, ""); w.Code != 200 || w.Header().Get("ETag") != `"1"` || !strings.Contains(w.Body.String(), `"source"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w = call(h, "PUT", "/v1/apps/"+created.ID, body(source)); w.Code != 428 {
		t.Fatalf("replace without If-Match: %d %s", w.Code, w.Body)
	}
	changed := strings.Replace(source, `"Hello"`, `"Hello again"`, 1)
	if w = call(h, "PUT", "/v1/apps/"+created.ID, body(changed), "If-Match", `"1"`); w.Code != 200 || w.Header().Get("ETag") != `"2"` || !strings.Contains(w.Body.String(), `"current_version":2`) {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	if w = call(h, "PUT", "/v1/apps/"+created.ID, body(changed), "If-Match", `"1"`); w.Code != 412 {
		t.Fatalf("a stale replace: %d %s", w.Code, w.Body)
	}

	w = call(h, "GET", "/v1/apps/"+created.ID+"/bundle", "")
	hash := w.Header().Get("ETag")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("PaperGo-App-Version") != "2" ||
		!strings.Contains(w.Body.String(), "Hello again") || len(hash) != 66 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("bundle: %d %v %s", w.Code, w.Header(), w.Body)
	}
	if w = call(h, "GET", "/v1/apps/"+created.ID+"/bundle", "", "If-None-Match", hash); w.Code != 304 || w.Body.Len() != 0 {
		t.Fatalf("an unchanged bundle: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", "/v1/apps/"+created.ID+"/versions", ""); w.Code != 200 || strings.Count(w.Body.String(), `"number"`) != 2 {
		t.Fatalf("versions: %d %s", w.Code, w.Body)
	}
	if w = call(h, "DELETE", "/v1/apps/"+created.ID, "", "If-Match", `"2"`); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", "/v1/apps/"+created.ID, ""); w.Code != 404 {
		t.Fatalf("a deleted app: %d %s", w.Code, w.Body)
	}
}
