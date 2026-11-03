package main

import (
	"bytes"
	"context"
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
	if err := admin(context.Background(), health, new(bytes.Buffer)); err != nil {
		t.Fatal(err)
	}
	ready.Store(false)
	if err := admin(context.Background(), health, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("not ready: %v", err)
	}
	if err := admin(context.Background(), []string{"health", "extra"}, new(bytes.Buffer)); err == nil {
		t.Fatal("positional argument accepted")
	}
}

func TestAdminUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}} {
		if err := admin(context.Background(), args, new(bytes.Buffer)); err == nil || !strings.Contains(err.Error(), "usage: papergo admin") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
