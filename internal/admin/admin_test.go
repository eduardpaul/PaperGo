package admin

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	// It does not need a configuration that loads.
	t.Setenv("APP_ENV", "staging")
	for _, args := range [][]string{{"--help"}, {"health", "--help"}, {"backup", "--help"}, {"initialize", "--help"}, {"workers", "status", "--help"}} {
		out := new(bytes.Buffer)
		if err := Run(context.Background(), args, out); !errors.Is(err, flag.ErrHelp) || out.Len() == 0 {
			t.Fatalf("%v: %v %q", args, err, out)
		}
	}
}

// The commands act on the data the server's configuration names: a
// deployment configured with config.yaml alone needs no --database.
func TestAdminCommandsReadTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	path := filepath.Join(dir, "papergo.yaml")
	if err := os.WriteFile(path, []byte("server:\n  data_dir: "+data+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAPERGO_CONFIG", path)
	for _, key := range []string{"APP_ENV", "DATABASE_PATH", "BLOB_PATH", "SEARCH_DATABASE_PATH", "DATABASE_URL", "HTTP_ADDR"} {
		t.Setenv(key, "")
	}
	ctx := context.Background()
	if err := Run(ctx, []string{"initialize"}, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "papergo.db")); err != nil {
		t.Fatal("initialize did not create the configured database:", err)
	}
	if err := os.MkdirAll(filepath.Join(data, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "backup.tar.gz")
	if err := Run(ctx, []string{"backup", "--file", archive}, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(archive); err != nil || info.Size() == 0 {
		t.Fatal("backup of the configured data:", err)
	}
	out := new(bytes.Buffer)
	if err := Run(ctx, []string{"workers", "status"}, out); err != nil {
		t.Fatal(err)
	}
	// A configuration that does not load stops the command, saying so.
	t.Setenv("APP_ENV", "staging")
	if err := Run(ctx, []string{"backup", "--file", archive}, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "read the configuration") {
		t.Fatalf("invalid configuration: %v", err)
	}
}
