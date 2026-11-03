package admin

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// admin health succeeds only while the server reports ready, which is what
// the container health check runs.
func TestAdminHealth(t *testing.T) {
	var ready atomic.Bool
	ready.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/ready" || !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	health := []string{"health", "--url", server.URL + "/health/ready"}
	if err := Run(context.Background(), health, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	ready.Store(false)
	if err := Run(context.Background(), health, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("not ready: %v", err)
	}
	if err := Run(context.Background(), []string{"health", "extra"}, new(bytes.Buffer)); err == nil {
		t.Fatal("positional argument accepted")
	}
	// Without --url it probes the configured listen address.
	ready.Store(true)
	t.Setenv("PAPERGO_CONFIG", "")
	t.Setenv("HTTP_ADDR", strings.TrimPrefix(server.URL, "http://"))
	if err := Run(context.Background(), []string{"health"}, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
}

func TestReadinessURL(t *testing.T) {
	for listen, want := range map[string]string{
		"127.0.0.1:8080": "http://127.0.0.1:8080/health/ready",
		"0.0.0.0:9090":   "http://127.0.0.1:9090/health/ready",
		":9090":          "http://127.0.0.1:9090/health/ready",
		"[::]:9090":      "http://[::1]:9090/health/ready",
	} {
		if got, err := readinessURL(listen); err != nil || got != want {
			t.Errorf("%s: %s %v", listen, got, err)
		}
	}
	if _, err := readinessURL("no-port"); err == nil {
		t.Error("address without a port accepted")
	}
}

func TestAdminUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}} {
		if err := Run(context.Background(), args, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "usage: papergo admin") {
			t.Fatalf("%v: %v", args, err)
		}
	}
	// Help is printed and reported as flag.ErrHelp, which main treats as
	// success.
	for _, args := range [][]string{{"--help"}, {"health", "--help"}, {"backup", "--help"}, {"workers", "status", "--help"}} {
		out := new(bytes.Buffer)
		if err := Run(context.Background(), args, out); !errors.Is(err, flag.ErrHelp) || out.Len() == 0 {
			t.Fatalf("%v: %v %q", args, err, out)
		}
	}
}
