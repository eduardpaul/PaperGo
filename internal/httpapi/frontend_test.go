package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestPublicFrontendAndReservedNamespaces(t *testing.T) {
	h, _, _ := setup(t)
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"bearer"}` {
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
