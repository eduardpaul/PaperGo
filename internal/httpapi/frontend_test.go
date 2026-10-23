package httpapi

import (
	"log/slog"
	"net/http"
	"net/url"
	"papergo/internal/auth"
	"strings"
	"testing"
)

func TestAuthConfigNamesTheDeploymentsFeatures(t *testing.T) {
	_, s, _ := setup(t)
	endpoint, _ := url.Parse("https://docs.example.com/v1/mcp")
	h := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Logger: slog.Default(), MCPURL: endpoint, CommentsOff: true}).Handler()
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"bearer","features":{"comments":false,"mcp":{"url":"https://docs.example.com/v1/mcp"}}}` {
		t.Fatal(config.Code, config.Body.String())
	}
	if w := request(h, "GET", "/v1/items/x/comments", "", testToken, "", ""); w.Code != 404 {
		t.Fatalf("comment routes without comments: %d", w.Code)
	}
}

func TestPublicFrontendAndReservedNamespaces(t *testing.T) {
	h, _, _ := setup(t)
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"bearer","features":{"comments":true}}` {
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
	want := `{"mode":"bearer","oidc":{"issuer":"https://auth.example.com/application/o/papergo/","client_id":"papergo","scope":"openid profile email offline_access"},"features":{"comments":true}}`
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != want || config.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("browser sign-in discovery", config.Code, config.Body.String(), config.Header())
	}
}
