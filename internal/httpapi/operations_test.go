package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	directorydomain "papergo/internal/dal/directory"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/storage"
)

type testModule struct{ routes []Route }

func (m testModule) Name() string    { return "test" }
func (m testModule) Routes() []Route { return m.routes }

func TestModuleRoutes(t *testing.T) {
	db := daltest.Database(t)
	s := dms.NewService(db.SQL)
	echo := Route{Method: "POST", Pattern: "/v1/test-module/{id}", OperationID: "testModule", Handle: func(w http.ResponseWriter, r *http.Request, in Request) error {
		if r.PathValue("id") == "missing" {
			return dms.ErrNotFound
		}
		respond(w, r, 200, map[string]string{"subject": in.Subject, "id": r.PathValue("id")})
		return nil
	}}
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), Modules: []Module{testModule{[]Route{echo}}}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	if res := request(h, "POST", "/v1/test-module/one", "", "", "", ""); res.Code != 401 {
		t.Fatal("unauthenticated module route", res.Code)
	}
	res := request(h, "POST", "/v1/test-module/one", "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"subject":"alice"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request(h, "POST", "/v1/test-module/missing", "", testToken, "", ""); res.Code != 404 || !strings.Contains(res.Body.String(), `"code":"not_found"`) {
		t.Fatal("module errors map like core errors", res.Code, res.Body.String())
	}
	// Module routes accept idempotency keys like core routes.
	keyed := httptest.NewRequest("POST", "/v1/test-module/one", nil)
	keyed.Header.Set("Authorization", "Bearer "+testToken)
	keyed.Header.Set("Idempotency-Key", "has space")
	invalidKey := httptest.NewRecorder()
	h.ServeHTTP(invalidKey, keyed)
	if invalidKey.Code != 400 {
		t.Fatal("module route ignored Idempotency-Key", invalidKey.Code)
	}
	for name, routes := range map[string][]Route{
		"core route":   {{Method: "GET", Pattern: "/v1/workspaces", OperationID: "shadow", Handle: echo.Handle}},
		"two modules":  {echo, {Method: "POST", Pattern: "/v1/test-module/{other}", OperationID: "other", Handle: echo.Handle}},
		"operation ID": {echo, {Method: "GET", Pattern: "/v1/test-module/{id}", OperationID: "testModule", Handle: echo.Handle}},
		"outside /v1":  {{Method: "GET", Pattern: "/health/module", OperationID: "health", Handle: echo.Handle}},
		"batch":        {{Method: "POST", Pattern: "/v1/$batch", OperationID: "batch", Handle: echo.Handle}},
	} {
		a.Modules = []Module{testModule{routes}}
		if err := a.Validate(); err == nil {
			t.Fatalf("%s: conflicting module route accepted", name)
		}
	}
}

func TestOperationRequestsAreIdempotentCancellableAndBounded(t *testing.T) {
	h, s, path := setup(t)
	files, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RegisterPortability(files); err != nil {
		t.Fatal(err)
	}
	directory := s.Directory()
	role, err := directory.SaveRole(t.Context(), "organization-admin", "", 0, identity.RoleInput{Name: "Exporter", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(t.Context(), "organization-admin", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	export := func(key, body string) (*http.Response, dms.OperationView) {
		t.Helper()
		r, _ := http.NewRequest("POST", "/v1/portability/exports", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+testToken)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var op dms.OperationView
		_ = json.Unmarshal(w.Body.Bytes(), &op)
		return w.Result(), op
	}
	res, first := export("retry-1", "")
	if res.StatusCode != 202 || res.Header.Get("Location") != "/v1/operations/"+first.ID {
		t.Fatal(res.StatusCode, first)
	}
	res, again := export("retry-1", "")
	if res.StatusCode != 200 || res.Header.Get("Idempotent-Replayed") != "true" || again.ID != first.ID {
		t.Fatal("replay", res.StatusCode, again)
	}
	if res, _ = export("retry-1", `{"include_data":false}`); res.StatusCode != 409 {
		t.Fatal("changed request with the same key", res.StatusCode)
	}
	if res, _ = export("not valid", ""); res.StatusCode != 400 {
		t.Fatal("invalid key", res.StatusCode)
	}
	cancel := request(h, "POST", "/v1/operations/"+first.ID+"/cancel", "", testToken, "", "")
	var cancelled dms.OperationView
	if cancel.Code != 200 || json.Unmarshal(cancel.Body.Bytes(), &cancelled) != nil || cancelled.Status != operations.StatusCancelled {
		t.Fatal(cancel.Code, cancel.Body.String())
	}
	if cancel = request(h, "POST", "/v1/operations/"+first.ID+"/cancel", "", testToken, "", ""); cancel.Code != 409 {
		t.Fatal("cancelled twice", cancel.Code)
	}
	s.SetOperationQuotas(dms.OperationQuotas{PerCreator: 1})
	if res, _ = export("", ""); res.StatusCode != 202 {
		t.Fatal(res.StatusCode)
	}
	res, _ = export("", "")
	if res.StatusCode != 429 || res.Header.Get("Retry-After") != "60" {
		t.Fatal("quota", res.StatusCode)
	}
}
