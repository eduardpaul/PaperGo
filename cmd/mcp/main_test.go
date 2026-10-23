package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func environment(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestConfigure(t *testing.T) {
	cfg, tools, listen, enabled, err := configure(environment(map[string]string{
		"PAPERGO_MCP_BACKEND_URL": "http://papergo:8080", "PAPERGO_MCP_PUBLIC_URL": "https://papergo.example/v1/mcp",
		"PAPERGO_MCP_ALLOWED_ORIGINS": "https://app.example", "PAPERGO_MCP_ENABLED_EXTENSIONS": "acme.crm, other",
	}))
	if err != nil || cfg.BackendURL.String() != "http://papergo:8080" || cfg.PublicURL.Host != "papergo.example" || listen != "127.0.0.1:8081" ||
		strings.Join(enabled, ",") != "acme.crm,other" || strings.Join(cfg.AllowedOrigins, ",") != "https://app.example" ||
		tools.AppURL.String() != "https://papergo.example" {
		t.Fatalf("%+v %+v %s %v %v", cfg, tools, listen, enabled, err)
	}
	// Links follow the endpoint's host unless the web app is elsewhere.
	_, tools, _, _, err = configure(environment(map[string]string{"PAPERGO_MCP_BACKEND_URL": "http://papergo:8080",
		"PAPERGO_MCP_PUBLIC_URL": "https://mcp.example/v1/mcp", "PAPERGO_MCP_APP_URL": "https://papergo.example/"}))
	if err != nil || tools.AppURL.String() != "https://papergo.example" {
		t.Fatalf("%+v %v", tools, err)
	}
	if _, tools, _, _, err = configure(environment(map[string]string{"PAPERGO_MCP_BACKEND_URL": "http://papergo:8080"})); err != nil || tools.AppURL != nil {
		t.Fatalf("%+v %v", tools, err)
	}
	for name, values := range map[string]map[string]string{
		"no backend":       {},
		"public backend":   {"PAPERGO_MCP_BACKEND_URL": "http://papergo.example"},
		"backend path":     {"PAPERGO_MCP_BACKEND_URL": "https://papergo.example/api"},
		"public URL path":  {"PAPERGO_MCP_BACKEND_URL": "http://papergo", "PAPERGO_MCP_PUBLIC_URL": "https://papergo.example/mcp"},
		"origin with path": {"PAPERGO_MCP_BACKEND_URL": "http://papergo", "PAPERGO_MCP_ALLOWED_ORIGINS": "https://app.example/x"},
		"listen":           {"PAPERGO_MCP_BACKEND_URL": "http://papergo", "PAPERGO_MCP_LISTEN": "8081"},
		"app URL path":     {"PAPERGO_MCP_BACKEND_URL": "http://papergo", "PAPERGO_MCP_APP_URL": "https://papergo.example/app"},
		"public http app":  {"PAPERGO_MCP_BACKEND_URL": "http://papergo", "PAPERGO_MCP_APP_URL": "http://papergo.example"},
	} {
		if _, _, _, _, err := configure(environment(values)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestRunServesAndDrains(t *testing.T) {
	release := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	addr := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, environment(map[string]string{"PAPERGO_MCP_BACKEND_URL": api.URL, "PAPERGO_MCP_LISTEN": "127.0.0.1:0"}),
			slog.New(slog.NewTextHandler(io.Discard, nil)), func(a net.Addr) { addr <- a })
	}()
	base := "http://" + (<-addr).String()
	res, err := http.Get(base + "/health/live")
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("live: %v %v", res, err)
	}
	res.Body.Close()
	// A request in flight finishes after shutdown begins.
	inFlight := make(chan int, 1)
	go func() {
		res, err := http.Get(base + "/health/ready")
		if err != nil {
			inFlight <- 0
			return
		}
		res.Body.Close()
		inFlight <- res.StatusCode
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(base, "http://"), time.Second); err == nil {
		t.Error("still admitting after shutdown began")
	}
	close(release)
	if code := <-inFlight; code != http.StatusNoContent {
		t.Fatalf("in-flight request: %d", code)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
