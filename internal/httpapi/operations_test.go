package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
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
		if r.PathValue("id") == "taken" {
			return &Problem{Status: 409, Code: "taken", Detail: "taken", Extensions: map[string]any{"holder_id": "h1"}}
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
	// A module's problem can name what it conflicts with, as core conflicts do.
	if res = request(h, "POST", "/v1/test-module/taken", "", testToken, "", ""); res.Code != 409 || !strings.Contains(res.Body.String(), `"code":"taken"`) || !strings.Contains(res.Body.String(), `"holder_id":"h1"`) {
		t.Fatal("a problem's extensions", res.Code, res.Body.String())
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

// A module's in-process requests act as its caller, with the credential's
// scopes, whatever context the module makes them with; Bearer tells a
// credential the client sent from a proxy's assertion.
func TestModuleLoopbackActsAsTheCaller(t *testing.T) {
	_, s, _ := setup(t)
	me := Route{Method: "POST", Pattern: "/v1/test-module/me", OperationID: "testModuleMe", Handle: func(w http.ResponseWriter, r *http.Request, in Request) error {
		sub := httptest.NewRequestWithContext(context.Background(), "GET", "/v1/identity/me", nil)
		got := httptest.NewRecorder()
		in.Loopback().ServeHTTP(got, sub)
		var caller struct {
			Subject string   `json:"subject"`
			Scopes  []string `json:"scopes"`
		}
		if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &caller) != nil {
			return fmt.Errorf("identity: %d %s", got.Code, got.Body.String())
		}
		scopes, err := in.Scopes(context.Background())
		if err != nil || !slices.Equal(scopes, caller.Scopes) {
			return fmt.Errorf("scopes %v, identity reports %v: %v", scopes, caller.Scopes, err)
		}
		respond(w, r, 200, map[string]any{"bearer": in.Bearer(), "subject": caller.Subject, "scopes": caller.Scopes})
		return nil
	}}
	token, err := s.Directory().CreateAccessToken(t.Context(), "bob", identity.AccessTokenInput{Name: "Assistant", Scopes: []string{"item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), Modules: []Module{testModule{[]Route{me}}}}).Handler()
	if res := request(h, "POST", "/v1/test-module/me", "", testToken, "", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"bearer":true`) || !strings.Contains(res.Body.String(), `"subject":"alice"`) {
		t.Fatal("bearer-mode caller", res.Code, res.Body.String())
	}
	if res := request(h, "POST", "/v1/test-module/me", "", token.Secret, "", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"bearer":true,"scopes":["item.read"],"subject":"bob"`) {
		t.Fatal("access token's scopes", res.Code, res.Body.String())
	}
	// Bearer follows what authentication accepted, however the header is
	// spaced.
	tabbed := httptest.NewRequest("POST", "/v1/test-module/me", nil)
	tabbed.Header.Set("Authorization", "Bearer\t"+testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, tabbed)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"bearer":true`) {
		t.Fatal("tab-separated bearer credential", res.Code, res.Body.String())
	}
	secret := strings.Repeat("s", 64)
	proxy, err := auth.NewProxy(secret, []netip.Prefix{netip.MustParsePrefix("172.29.242.2/32")}, "https://docs.example.com")
	if err != nil {
		t.Fatal(err)
	}
	h = (&API{DMS: s, Auth: proxy, Logger: slog.Default(), Modules: []Module{testModule{[]Route{me}}}}).Handler()
	asserted := httptest.NewRequest("POST", "/v1/test-module/me", nil)
	asserted.RemoteAddr = "172.29.242.2:1234"
	asserted.Header.Set(auth.ProxySubjectHeader, "bob")
	asserted.Header.Set(auth.ProxySecretHeader, secret)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, asserted)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"bearer":false`) {
		t.Fatal("proxy assertion is not a bearer credential", res.Code, res.Body.String())
	}
	if res = request(h, "POST", "/v1/test-module/me", "", token.Secret, "", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"bearer":true`) {
		t.Fatal("access token in a proxy deployment", res.Code, res.Body.String())
	}
	// A refused bearer credential is challenged as one, not with the
	// proxy's Basic sign-in.
	if res = request(h, "POST", "/v1/test-module/me", "", "pgo_revoked", "", ""); res.Code != 401 || res.Header().Get("WWW-Authenticate") != `Bearer realm="PaperGo", error="invalid_token"` {
		t.Fatal("refused bearer challenge", res.Code, res.Header())
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
