package documentpreview_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kolesa-team/go-webp/webp"
	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"papergo/internal/doctext/doctexttest"
	"papergo/internal/documentpreview"
	"papergo/internal/httpapi"
	"papergo/internal/imaging/imagingtest"
	"papergo/internal/storage"
)

// Pages render on request at shared widths: every page of a PDF or TIFF,
// for the file the caller reads or an earlier version.
func TestPageRenditions(t *testing.T) {
	ctx := context.Background()
	s := dms.NewService(daltest.Database(t).SQL)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &documentpreview.Renderer{Storage: store, PDF: doctexttest.PDF(t)}
	if err = r.Register(s); err != nil {
		t.Fatal(err)
	}
	h := (&httpapi.API{DMS: s, Auth: auth.Development{Token: token, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), MaxUpload: 8 << 20, Modules: []httpapi.Module{r.Module()}}).Handler()
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
	major := "major"
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Scans", Versioning: &major})
	if err != nil {
		t.Fatal(err)
	}
	upload := func(item *resources.Resource, version int, name, media string, data []byte) {
		t.Helper()
		if res := call("PUT", "/v1/items/"+item.ID+"/content", string(data), "If-Match", `"`+strconv.Itoa(version)+`"`, "Content-Type", media, "X-Filename", name); res.Code != 201 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	list := func(id, query string) documentpreview.PageList {
		t.Helper()
		res := call("GET", "/v1/items/"+id+"/pages"+query, "")
		var out documentpreview.PageList
		if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		return out
	}
	page := func(path string, want int) (int, *httptest.ResponseRecorder) {
		t.Helper()
		res := call("GET", path, "")
		if res.Code != want {
			t.Fatal(path, res.Code, res.Body.String())
		}
		if want != 200 {
			return 0, res
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(res.Body.Bytes()), nil)
		if err != nil || res.Header().Get("Content-Type") != "image/webp" || res.Header().Get("Content-Security-Policy") == "" {
			t.Fatal(path, err, res.Header())
		}
		return cfg.Width, res
	}

	scan, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "scan.tif"})
	if err != nil {
		t.Fatal(err)
	}
	upload(scan, 1, "scan.tif", "image/tiff", imagingtest.TIFF(imagingtest.Page(1000, 1400, 255), imagingtest.Page(300, 200, 0), imagingtest.Page(50, 50, 128)))
	pages := list(scan.ID, "")
	if len(pages.Pages) != 3 || pages.Pages[0] != (documentpreview.PageSize{Width: 1000, Height: 1400}) || pages.Pages[1].Width != 300 || pages.Truncated {
		t.Fatalf("%+v", pages)
	}
	// Widths snap to the shared steps, and images are never enlarged.
	if w, _ := page("/v1/items/"+scan.ID+"/pages/1?width=500", 200); w != 640 {
		t.Fatal("width", w)
	}
	if w, _ := page("/v1/items/"+scan.ID+"/pages/2?width=2000", 200); w != 300 {
		t.Fatal("an image was enlarged", w)
	}
	_, first := page("/v1/items/"+scan.ID+"/pages/3", 200)
	if res := call("GET", "/v1/items/"+scan.ID+"/pages/3", "", "If-None-Match", first.Header().Get("ETag")); res.Code != 304 {
		t.Fatal("a repeated page was not cached by the browser", res.Code)
	}
	page("/v1/items/"+scan.ID+"/pages/4", 404)
	page("/v1/items/"+scan.ID+"/pages/0", 404)
	page("/v1/items/"+scan.ID+"/pages/1?width=-3", 422)

	// The first PDF version, then a replacement: the earlier one stays readable by its blob ID.
	doc, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "contract.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	upload(doc, 1, "contract.pdf", "application/pdf", doctexttest.TextPDF("First", "Second"))
	earlier := list(doc.ID, "")
	if len(earlier.Pages) != 2 || earlier.Pages[0] != (documentpreview.PageSize{Width: 816, Height: 1056}) {
		t.Fatalf("%+v", earlier)
	}
	if w, _ := page("/v1/items/"+doc.ID+"/pages/2?width=320", 200); w != 320 {
		t.Fatal("pdf width", w)
	}
	current, err := s.Get(ctx, "alice", doc.ID)
	if err != nil {
		t.Fatal(err)
	}
	upload(doc, current.Version, "contract.pdf", "application/pdf", doctexttest.TextPDF("Only"))
	if now := list(doc.ID, ""); len(now.Pages) != 1 || now.BlobID == earlier.BlobID {
		t.Fatalf("%+v", now)
	}
	if again := list(doc.ID, "?blob_id="+earlier.BlobID); len(again.Pages) != 2 {
		t.Fatalf("%+v", again)
	}
	if _, res := page("/v1/items/"+doc.ID+"/pages/2?blob_id="+earlier.BlobID, 200); !strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(res.Header())
	}

	// Other files have no pages to render.
	other, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "notes.zip"})
	if err != nil {
		t.Fatal(err)
	}
	upload(other, 1, "notes.zip", "application/zip", []byte("PK\x03\x04zip"))
	if res := call("GET", "/v1/items/"+other.ID+"/pages", ""); res.Code != 415 {
		t.Fatal(res.Code, res.Body.String())
	}
	broken, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "broken.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	upload(broken, 1, "broken.pdf", "application/pdf", []byte("%PDF-broken"))
	if res := call("GET", "/v1/items/"+broken.ID+"/pages", ""); res.Code != 422 || !strings.Contains(res.Body.String(), "unreadable_file") {
		t.Fatal(res.Code, res.Body.String())
	}
}
