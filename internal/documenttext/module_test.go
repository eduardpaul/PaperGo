package documenttext_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/documenttext"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const token = "0123456789abcdef0123456789abcdef"

func call(h http.Handler, method, path, body, contentType string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Clients start reading a file's text as an operation, through the module's
// route, and follow it. The operation keeps the file version it accepted.
func TestReadTextRoute(t *testing.T) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reader := &documenttext.Reader{Storage: store}
	if err = reader.Register(s); err != nil {
		t.Fatal(err)
	}
	a := &httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxUpload: 1 << 20, Modules: []httpapi.Module{reader.Module()}}
	if err = a.Validate(); err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Letters"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Letter"})
	if err != nil {
		t.Fatal(err)
	}
	upload := httptest.NewRequest("PUT", "/v1/items/"+item.ID+"/content", strings.NewReader("%PDF-letter bytes"))
	upload.Header.Set("Authorization", "Bearer "+token)
	upload.Header.Set("If-Match", `"1"`)
	upload.Header.Set("Content-Type", "application/pdf")
	upload.Header.Set("X-Filename", "letter.pdf")
	uploaded := httptest.NewRecorder()
	h.ServeHTTP(uploaded, upload)
	if uploaded.Code != 201 {
		t.Fatal(uploaded.Code, uploaded.Body.String())
	}
	path := "/v1/items/" + item.ID + "/text"
	if res := call(h, "POST", path, `{"ocr":"always"}`, "application/json"); res.Code != 422 {
		t.Fatal("ocr=always without an engine:", res.Code, res.Body.String())
	}
	res := call(h, "POST", path, "", "", "Idempotency-Key", "read-1")
	var op dms.OperationView
	if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil || op.Status != operations.StatusPending || op.Kind != documenttext.Kind || res.Header().Get("Location") != "/v1/operations/"+op.ID {
		t.Fatal(res.Code, res.Header(), res.Body.String())
	}
	if again := call(h, "POST", path, "", "", "Idempotency-Key", "read-1"); again.Code != 200 || !strings.Contains(again.Body.String(), op.ID) {
		t.Fatal("replay", again.Code, again.Body.String())
	}
	// The accepted file version is leased until the operation ends.
	if n := daltest.Count(t, s.DAL, "blob_leases", daltest.Row{"operation_id": op.ID}); n != 1 {
		t.Fatalf("%d leases for the accepted file", n)
	}
	if res = call(h, "POST", "/v1/operations/"+op.ID+"/cancel", "", ""); res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	if n := daltest.Count(t, s.DAL, "blob_leases", daltest.Row{"operation_id": op.ID}); n != 0 {
		t.Fatalf("%d leases after the operation ended", n)
	}
}
