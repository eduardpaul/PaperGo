package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimitShedsExcessAndExemptsBlobTransfers(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 4)
	a := &API{Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), MaxInFlight: 1}
	h := a.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(204)
	}))
	done := make(chan int)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/workspaces", nil))
		done <- w.Code
	}()
	<-entered
	// A blob transfer is not counted against, or blocked by, the slot.
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/items/x/content", nil))
		done <- w.Code
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("blob transfer waited for an API slot")
	}
	start := time.Now()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/workspaces", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("excess request: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if waited := time.Since(start); waited < admissionWait/2 || waited > 3*admissionWait {
		t.Fatalf("shed after %v, want about %v", waited, admissionWait)
	}
	close(release)
	for i := 0; i < 2; i++ {
		if code := <-done; code != 204 {
			t.Fatal("admitted request failed", code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/workspaces", nil))
	if w.Code != 204 {
		t.Fatal("slot not released", w.Code)
	}
}

func TestRequestTimeoutBecomesRetryable503(t *testing.T) {
	a := &API{Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), RequestTimeout: 20 * time.Millisecond}
	h := a.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		a.failure(w, r, r.Context().Err())
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/v1/workspaces", nil))
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("timeout: %d %s", w.Code, w.Body.String())
	}
	// A deadline that is not the request's own stays an internal error.
	w = httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/workspaces", nil)
	a.failure(w, r, context.DeadlineExceeded)
	if w.Code != 500 {
		t.Fatal("foreign deadline mapped as request timeout", w.Code)
	}
}
