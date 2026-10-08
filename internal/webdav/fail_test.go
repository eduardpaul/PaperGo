package webdav

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimedOutRequestsAreRetryable(t *testing.T) {
	h := &Handler{Logger: slog.Default()}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	w := httptest.NewRecorder()
	h.fail(w, httptest.NewRequest("PROPFIND", Prefix, nil).WithContext(ctx), context.DeadlineExceeded)
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Header())
	}
}
