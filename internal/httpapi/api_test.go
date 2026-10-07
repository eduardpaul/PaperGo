package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/testutil"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef"

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func setup(t *testing.T) (http.Handler, *dms.Service, string) {
	t.Helper()
	db := testutil.Database(t)
	path := t.TempDir()
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.Client)
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), Ready: db.SQL.PingContext, MaxUpload: 32}
	return a.Handler(), s, path
}
func request(h http.Handler, method, path, body, token, match, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("X-Filename", "contract.txt")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAuthenticationAndPreconditions(t *testing.T) {
	h, s, _ := setup(t)
	w := request(h, "GET", "/health/live", "", "", "", "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = request(h, "GET", "/v1/workspaces", "", "", "", "")
	if w.Code != 401 || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing authentication or request ID")
	}
	w = request(h, "POST", "/v1/workspaces", `{"name":"Demo","unknown":true}`, testToken, "", "application/json")
	if w.Code != 400 {
		t.Fatal("unknown JSON field accepted")
	}
	root, err := s.Create(context.Background(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "PATCH", "/v1/resources/"+root.ID, `{"name":"Changed"}`, testToken, "", "application/json")
	if w.Code != 428 {
		t.Fatal(w.Code)
	}
	w = request(h, "PATCH", "/v1/resources/"+root.ID, `{"name":"Changed"}`, testToken, `"1"`, "application/json")
	if w.Code != 200 || w.Header().Get("ETag") != `"2"` {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PATCH", "/v1/resources/"+root.ID, `{"name":"Lost"}`, testToken, `"1"`, "application/json")
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestBlobStreamingRevisionAndLimits(t *testing.T) {
	h, s, path := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Contract"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/items/" + item.ID + "/content"
	response := request(h, "PUT", endpoint, "signed contract", testToken, `"1"`, "text/plain")
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var blob ent.Blob
	if err = json.Unmarshal(response.Body.Bytes(), &blob); err != nil {
		t.Fatal(err)
	}
	pub, err := s.Publish(ctx, "alice", item.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err = json.Unmarshal(pub.Snapshot, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["blob_id"] != blob.ID {
		t.Fatal("publication omitted blob revision")
	}
	response = request(h, "PUT", endpoint, "new contract", testToken, `"3"`, "text/plain")
	if response.Code != 201 {
		t.Fatal(response.Body.String())
	}
	response = request(h, "GET", endpoint+"?blob_id="+blob.ID, "", testToken, "", "")
	if response.Code != 200 || response.Body.String() != "signed contract" {
		t.Fatal("old blob not retained", response.Body.String())
	}
	r := httptest.NewRequest("GET", endpoint, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Range", "bytes=0-2")
	ranged := httptest.NewRecorder()
	h.ServeHTTP(ranged, r)
	if ranged.Code != 206 || !bytes.Equal(ranged.Body.Bytes(), []byte("new")) {
		t.Fatal("range download failed", ranged.Code)
	}
	response = request(h, "PUT", endpoint, strings.Repeat("x", 33), testToken, `"4"`, "text/plain")
	if response.Code != 413 {
		t.Fatal("upload limit ignored", response.Code)
	}
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 2 {
		t.Fatalf("failed upload left object: %d %v", len(files), err)
	}
	response = request(h, "PUT", endpoint, "stale", testToken, `"2"`, "text/plain")
	if response.Code != 409 {
		t.Fatal("stale upload accepted")
	}
}
