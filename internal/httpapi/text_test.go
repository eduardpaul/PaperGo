package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
)

// saveText stores page texts for a file's bytes, as document.read_text does.
func saveText(t *testing.T, s *dms.Service, store storage.Store, sha string, pages string) *ent.BlobText {
	t.Helper()
	obj, err := store.Put(context.Background(), bytes.NewReader([]byte(pages)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.SaveBlobText(context.Background(), dms.BlobTextInput{SHA256: sha, Settings: "test", ObjectKey: obj.Key, ObjectSize: obj.Size, ObjectSHA256: obj.SHA256,
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
	var blob ent.Blob
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &blob) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	if res = request(h, "GET", "/v1/items/"+item.ID+"/text", "", testToken, "", ""); res.Code != 404 || !strings.Contains(res.Body.String(), "text_not_read") {
		t.Fatal(res.Code, res.Body.String())
	}
	if _, err = s.SaveBlobText(ctx, dms.BlobTextInput{SHA256: strings.Repeat("0", 64), Settings: "test", ObjectKey: "k", Extractor: "test"}); err == nil {
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
	if n, err := s.Client.BlobText.Query().Count(ctx); err != nil || n != 0 {
		t.Fatalf("texts left: %d %v", n, err)
	}
	if _, err = store.Open(ctx, row.ObjectKey); err == nil {
		t.Fatal("the text object is kept")
	}
}
