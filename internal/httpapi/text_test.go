package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"papergo/internal/dal/blobs"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/documenttext"
	"papergo/internal/storage"
)

// saveText stores page texts for a file's bytes, as document.read_text does.
func saveText(t *testing.T, s *dms.Service, store storage.Store, sha string, pages string) *blobs.Text {
	t.Helper()
	obj, err := store.Put(context.Background(), bytes.NewReader([]byte(pages)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.DAL.Blobs().SaveText(context.Background(), blobs.TextInput{SHA256: sha, Settings: "test", ObjectKey: obj.Key, ObjectSize: obj.Size, ObjectSHA256: obj.SHA256,
		Pages: 1, Chars: 13, Language: "english", Extractor: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

// The text of the file a caller may read, and its lifetime: it lives while
// a file with its bytes does.
func TestItemTextAPI(t *testing.T) {
	h, s, path := setup(t)
	ctx := context.Background()
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
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
	res := request(h, "PUT", "/v1/items/"+item.ID+"/content", "%PDF-letter bytes", testToken, `"1"`, "application/pdf")
	var blob blobs.Blob
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &blob) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request(h, "GET", "/v1/items/"+item.ID+"/text", "", testToken, "", ""); res.Code != 404 || !strings.Contains(res.Body.String(), "text_not_read") {
		t.Fatal(res.Code, res.Body.String())
	}
	if _, err = s.DAL.Blobs().SaveText(ctx, blobs.TextInput{SHA256: strings.Repeat("0", 64), Settings: "test", ObjectKey: "k", Extractor: "test"}); err == nil {
		t.Fatal("text for bytes no file holds")
	}
	row := saveText(t, s, store, blob.Sha256, `{"pages":[{"page":1,"text":"Dear customer"}]}`)
	// The same settings keep the first text.
	if again := saveText(t, s, store, blob.Sha256, `{"pages":[]}`); again.ID != row.ID {
		t.Fatal("a second text for the same bytes and settings")
	}
	res = request(h, "GET", "/v1/items/"+item.ID+"/text", "", testToken, "", "")
	var out struct {
		TextID string `json:"text_id"`
		BlobID string `json:"blob_id"`
		Pages  []struct {
			Page int    `json:"page"`
			Text string `json:"text"`
		} `json:"pages"`
		Language string `json:"language"`
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil || out.TextID != row.ID || out.BlobID != blob.ID || len(out.Pages) != 1 || out.Pages[0].Text != "Dear customer" || out.Language != "english" {
		t.Fatal(res.Code, res.Body.String())
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Storage: store, Logger: slog.Default()}).Handler()
	if res = request(bob, "GET", "/v1/items/"+item.ID+"/text", "", testToken, "", ""); res.Code != 403 && res.Code != 404 {
		t.Fatal("another user read the text", res.Code)
	}

	// Purging the only file with these bytes frees the text and its object.
	current, err := s.Get(ctx, "alice", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "alice", item.ID, current.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err = s.PurgeDeleted(ctx, "alice", lib.ID, item.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = s.CleanupPurgedBlobs(ctx, store); err != nil {
		t.Fatal(err)
	}
	if n, err := s.DAL.Blobs().TextCount(ctx, ""); err != nil || n != 0 {
		t.Fatalf("texts left: %d %v", n, err)
	}
	if _, err = store.Open(ctx, row.ObjectKey); err == nil {
		t.Fatal("the text object is kept")
	}
}

// Clients start reading a file's text as an operation and follow it.
func TestReadTextAPI(t *testing.T) {
	_, s, path := setup(t)
	ctx := context.Background()
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), MaxUpload: 1 << 20}
	plain := *a
	unconfigured := plain.Handler()
	a.DocumentText = &documenttext.Reader{Storage: store}
	if err = a.DocumentText.Register(s); err != nil {
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
	if res := request(h, "PUT", "/v1/items/"+item.ID+"/content", "%PDF-letter bytes", testToken, `"1"`, "application/pdf"); res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	path = "/v1/items/" + item.ID + "/text"
	if res := request(unconfigured, "POST", path, "", testToken, "", ""); res.Code != 503 {
		t.Fatal(res.Code, res.Body.String())
	}
	if res := request(h, "POST", path, "", "", "", ""); res.Code != 401 {
		t.Fatal(res.Code)
	}
	if res := request(h, "POST", path, `{"ocr":"always"}`, testToken, "", "application/json"); res.Code != 422 {
		t.Fatal("ocr=always without an engine:", res.Code, res.Body.String())
	}
	res := request(h, "POST", path, "", testToken, "", "")
	var op struct {
		ID, Status, Kind string
	}
	if res.Code != 202 || json.Unmarshal(res.Body.Bytes(), &op) != nil || op.Status != "pending" || op.Kind != documenttext.Kind || res.Header().Get("Location") != "/v1/operations/"+op.ID {
		t.Fatal(res.Code, res.Header(), res.Body.String())
	}
	if res = request(h, "GET", "/v1/operations/"+op.ID, "", testToken, "", ""); res.Code != 200 || !strings.Contains(res.Body.String(), `"status":"pending"`) {
		t.Fatal(res.Code, res.Body.String())
	}
}
