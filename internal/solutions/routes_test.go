package solutions

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/httpapi"
)

const token = "0123456789abcdef0123456789abcdef"

func packed(t *testing.T, files fstest.MapFS) []byte {
	t.Helper()
	raw, err := Pack(solution(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func call(h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/zip")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// The solutions routes plan and apply a packed solution, answer 409 with
// the refusals when nothing was applied, and read what is installed.
func TestSolutionRoutes(t *testing.T) {
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: admin}, Logger: slog.Default(), Modules: []httpapi.Module{Module(nil)}}
	h := a.Handler()
	ws, err := s.Create(t.Context(), admin, "", dms.CreateResource{Kind: "workspace", Name: "Casa"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/workspaces/" + ws.ID + "/solutions"

	if w := call(h, "POST", base+"/plan", []byte("not a zip")); w.Code != 422 || !strings.Contains(w.Body.String(), "packed solution") {
		t.Fatalf("a body that is no solution: %d %s", w.Code, w.Body)
	}
	broken := edit("solution.json", `{"key": "tank_flag"`, `{"key": "flag"`)
	raw, _ := Pack(func() Solution { s, _ := Load(broken); return s }())
	if w := call(h, "POST", base+"/apply", raw); w.Code != 422 || !strings.Contains(w.Body.String(), `must start with \"tank_\"`) {
		t.Fatalf("a solution with problems: %d %s", w.Code, w.Body)
	}

	var result Result
	w := call(h, "POST", base+"/plan", packed(t, tank()))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Applied || len(result.Changes) != 8 {
		t.Fatalf("plan: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", base, nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"value":[]}` {
		t.Fatalf("a plan installed something: %d %s", w.Code, w.Body)
	}
	options := url.QueryEscape(`{"settings": {"capacity_l": 1000}, "paused": true}`)
	w = call(h, "POST", base+"/apply?options="+options, packed(t, tank()))
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || !result.Applied || len(result.Changes) != 8 {
		t.Fatalf("apply: %d %s", w.Code, w.Body)
	}
	if w = call(h, "POST", base+"/apply?options="+url.QueryEscape(`{"colour": "red"}`), packed(t, tank())); w.Code != 422 || !strings.Contains(w.Body.String(), "options") {
		t.Fatalf("unknown options: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", base, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"key":"tank"`) || !strings.Contains(w.Body.String(), `"paused":true`) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var in Installation
	if w = call(h, "GET", base+"/tank", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &in) != nil || in.Version != "1.0.0" || len(in.Objects) != 7 || in.Manifest.Key != "tank" {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", base+"/diesel", nil); w.Code != 404 {
		t.Fatalf("a solution that is not installed: %d %s", w.Code, w.Body)
	}
	if w = call(h, "GET", base+"/tank/status", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}

	refused := edit("solution.json", `"type": "datetime"`, `"type": "date"`)
	if w = call(h, "POST", base+"/apply", packed(t, refused)); w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Applied || len(result.Refusals) != 1 {
		t.Fatalf("a refused apply: %d %s", w.Code, w.Body)
	}

	// Status says what is no longer as the solution needs it.
	list := in.object(KindCollection, "readings")
	current, err := s.Get(t.Context(), admin, list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(t.Context(), admin, list.ID, current.Version, dms.DeleteOptions{Recursive: true}); err != nil {
		t.Fatal(err)
	}
	if w = call(h, "GET", base+"/tank/status", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "readings: the list cannot be read") {
		t.Fatalf("status after the list was deleted: %d %s", w.Code, w.Body)
	}
}
