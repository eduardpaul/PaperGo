package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"papergo/internal/auth"
	"strings"
	"testing"
)

// advertisedModule is a feature the web app adapts to.
type advertisedModule struct{ feature string }

func (m advertisedModule) Name() string    { return m.feature }
func (m advertisedModule) Feature() string { return m.feature }
func (advertisedModule) Routes() []Route   { return nil }

func TestAuthConfigNamesTheDeploymentsFeatures(t *testing.T) {
	_, s, _ := setup(t)
	public, _ := url.Parse("https://docs.example.com")
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), Modules: []Module{advertisedModule{"mcp"}}, PublicURL: public}).Handler()
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || discoveredCore(t, config.Body.String()) != `{"mode":"bearer","public_url":"https://docs.example.com","features":{"mcp":true}}` {
		t.Fatal(config.Code, config.Body.String())
	}
}

func TestPublicFrontendAndReservedNamespaces(t *testing.T) {
	h, _, _ := setup(t)
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || discoveredCore(t, config.Body.String()) != `{"mode":"bearer","features":{}}` {
		t.Fatal("bearer capability discovery", config.Code, config.Body.String())
	}
	for _, route := range []string{"/", "/login", "/admin/users", "/w/workspace/l/collection"} {
		w := request(h, "GET", route, "", "", "", "")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), `<div id="root">`) {
			t.Fatalf("public UI %s: %d %s", route, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Request-ID") == "" || w.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("frontend middleware/caching: %v", w.Header())
		}
	}
	for _, route := range []string{"/v1", "/v1/", "/v1/workspaces", "/v1/unknown"} {
		if w := request(h, "GET", route, "", "", "", ""); w.Code != 401 {
			t.Fatalf("API auth bypass %s: %d %s", route, w.Code, w.Body.String())
		}
	}
	if w := request(h, "GET", "/v1/unknown", "", testToken, "", ""); w.Code != 404 || !strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("unknown API route must stay JSON: %d %s", w.Code, w.Body.String())
	}
	for _, route := range []string{"/health", "/health/missing", "/webdav", "/webdav/", "/webdav/missing"} {
		if w := request(h, "GET", route, "", "", "", ""); w.Code != 404 || strings.Contains(w.Body.String(), "<html") {
			t.Fatalf("reserved namespace %s must not serve the SPA: %d %s", route, w.Code, w.Body.String())
		}
	}
}

func TestAuthConfigAdvertisesBrowserSignIn(t *testing.T) {
	_, s, _ := setup(t)
	signIn := &BrowserSignIn{Issuer: "https://auth.example.com/application/o/papergo/", ClientID: "papergo", Scope: "openid profile email offline_access"}
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), BrowserSignIn: signIn}).Handler()
	config := request(h, "GET", "/auth/config", "", "", "", "")
	want := `{"mode":"bearer","oidc":{"issuer":"https://auth.example.com/application/o/papergo/","client_id":"papergo","scope":"openid profile email offline_access"},"features":{}}`
	if config.Code != 200 || discoveredCore(t, config.Body.String()) != want || config.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("browser sign-in discovery", config.Code, config.Body.String(), config.Header())
	}
}

// discoveredCore is an /auth/config body without its file descriptions,
// which TestAuthConfigDescribesFiles checks.
func discoveredCore(t *testing.T, body string) string {
	t.Helper()
	var config struct {
		Mode      string                     `json:"mode"`
		PublicURL string                     `json:"public_url,omitempty"`
		OIDC      *BrowserSignIn             `json:"oidc,omitempty"`
		Features  map[string]json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatal(err)
	}
	delete(config.Features, "file_types")
	delete(config.Features, "file_edit_max_bytes")
	core, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return string(core)
}

func TestAuthConfigDescribesFiles(t *testing.T) {
	_, s, _ := setup(t)
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), FileEditMax: 1234}).Handler()
	var config struct {
		Features struct {
			FileTypes []struct {
				MediaType  string   `json:"media_type"`
				Label      string   `json:"label"`
				Extensions []string `json:"extensions"`
				Preview    bool     `json:"preview"`
				Text       bool     `json:"text"`
			} `json:"file_types"`
			FileEditMax int64 `json:"file_edit_max_bytes"`
		} `json:"features"`
	}
	res := request(h, "GET", "/auth/config", "", "", "", "")
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &config) != nil || config.Features.FileEditMax != 1234 {
		t.Fatal(res.Code, res.Body.String())
	}
	found := map[string]bool{}
	for _, f := range config.Features.FileTypes {
		found[f.MediaType] = true
		if f.Label == "" || f.Extensions == nil {
			t.Fatalf("%+v", f)
		}
		if f.MediaType == "application/pdf" && (!f.Preview || !f.Text) || f.MediaType == "application/zip" && (f.Preview || f.Text) {
			t.Fatalf("capabilities %+v", f)
		}
	}
	if !found["application/pdf"] || !found["application/vnd.openxmlformats-officedocument.wordprocessingml.document"] {
		t.Fatal(found)
	}
}
