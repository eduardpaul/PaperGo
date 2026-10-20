package documentfiles_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/documentfiles"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const token = "0123456789abcdef0123456789abcdef"

// The routes answer people and clients as they answer workflow runs.
func TestRoutesOutsideRuns(t *testing.T) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := &documentfiles.Maker{Storage: store}
	if err = m.Register(s); err != nil {
		t.Fatal(err)
	}
	h := (&httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxUpload: 1 << 20, Modules: []httpapi.Module{m.Module()}}).Handler()
	call := func(method, path, body string, header ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Photos"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Scan"})
	if err != nil {
		t.Fatal(err)
	}
	if res := call("POST", "/v1/items/"+item.ID+"/optimize-image", ""); res.Code != 422 || !strings.Contains(res.Body.String(), "no file") {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, path := range []string{"/v1/artifacts/x/stage", "/v1/staged-files/x/promote", "/v1/staged-files/x/discard"} {
		if res := call("POST", path, ""); res.Code != 404 {
			t.Fatal(path, res.Code, res.Body.String())
		}
	}
	for i, body := range []string{"%PDF-first", "%PDF-second"} {
		version := []string{`"1"`, `"2"`}[i]
		if res := call("PUT", "/v1/items/"+item.ID+"/content", body, "If-Match", version, "Content-Type", "application/pdf", "X-Filename", "scan.pdf"); res.Code != 201 && res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	versions, err := s.FileVersions(ctx, "alice", item.ID, 0, 10)
	if err != nil || len(versions.Data) != 2 {
		t.Fatalf("%+v %v", versions, err)
	}
	first := versions.Data[len(versions.Data)-1]
	res := call("POST", "/v1/items/"+item.ID+"/content/reclaim", `{"blob_id":"`+first.ID+`"}`, "Content-Type", "application/json")
	var reclaimed documentfiles.ReclaimResult
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &reclaimed) != nil || reclaimed.BlobID != first.ID {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = call("POST", "/v1/items/"+item.ID+"/content/reclaim", `{"blob_id":"missing"}`, "Content-Type", "application/json"); res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
}
