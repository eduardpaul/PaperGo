package documentpreview_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/documentpreview"
	"papergo/internal/httpapi"
	"papergo/internal/storage"
	"papergo/internal/worker"
)

func TestMain(m *testing.M) { daltest.Main(m) }

const token = "0123456789abcdef0123456789abcdef"

// A preview is rendered once per file bytes: rendering the same bytes again
// reuses it, and a file that cannot be rendered is recorded as such.
func TestRenderPreview(t *testing.T) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &documentpreview.Renderer{Storage: store}
	if err = r.Register(s); err != nil {
		t.Fatal(err)
	}
	h := (&httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxUpload: 1 << 20, Modules: []httpapi.Module{r.Module()}}).Handler()
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
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := range 40 {
		img.Set(x, x%30, color.Black)
	}
	var pic bytes.Buffer
	if err = png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	render := func(name, media string, data []byte) documentpreview.Result {
		t.Helper()
		item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if res := call("PUT", "/v1/items/"+item.ID+"/content", string(data), "If-Match", `"1"`, "Content-Type", media, "X-Filename", name); res.Code != 201 {
			t.Fatal(res.Code, res.Body.String())
		}
		res := call("POST", "/v1/items/"+item.ID+"/preview", "")
		var op dms.OperationView
		if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil || op.Kind != documentpreview.Kind {
			t.Fatal(res.Code, res.Body.String())
		}
		if err = worker.Drain(ctx, s); err != nil {
			t.Fatal(err)
		}
		done, err := s.Operation(ctx, "alice", op.ID)
		var result documentpreview.Result
		if err != nil || done.Status != operations.StatusSucceeded || json.Unmarshal(done.Result, &result) != nil {
			t.Fatalf("%+v %v", done, err)
		}
		if result.Ready {
			if got := call("GET", "/v1/items/"+item.ID+"/preview", ""); got.Code != 200 || got.Header().Get("Content-Type") != "image/webp" {
				t.Fatal(got.Code, got.Header())
			}
		}
		return result
	}
	if got := render("one.png", "image/png", pic.Bytes()); !got.Ready || got.Reused {
		t.Fatalf("%+v", got)
	}
	if got := render("two.png", "image/png", pic.Bytes()); !got.Ready || !got.Reused {
		t.Fatalf("the same bytes were rendered again: %+v", got)
	}
	if got := render("broken.pdf", "application/pdf", []byte("%PDF-broken")); got.Ready {
		t.Fatalf("%+v", got)
	}
}
