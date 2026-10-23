package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/internal/dal/blobs"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dms"
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

// Ranged reads return text within a character budget and say where to
// resume, inside a page when one is longer than the budget.
func TestTextRange(t *testing.T) {
	pages := []textPage{{Page: 1, Text: "ééééé"}, {Page: 2, Text: "abc"}, {Page: 3, Text: "xyz"}}
	render := func(out []textPage, next *textPosition) string {
		var b strings.Builder
		for _, p := range out {
			fmt.Fprintf(&b, "%d:%s:%v ", p.Page, p.Text, p.Partial)
		}
		if next != nil {
			fmt.Fprintf(&b, "next %d+%d", next.Page, next.Offset)
		}
		return strings.TrimSpace(b.String())
	}
	for _, c := range []struct {
		from  textPosition
		limit int
		want  string
	}{
		{textPosition{Page: 1}, 0, "1:ééééé:false 2:abc:false 3:xyz:false"},
		{textPosition{Page: 1}, 2, "1:éé:true next 1+2"},
		{textPosition{Page: 1, Offset: 2}, 2, "1:éé:true next 1+4"},
		{textPosition{Page: 1, Offset: 4}, 2, "1:é:true 2:a:true next 2+1"},
		{textPosition{Page: 1}, 8, "1:ééééé:false 2:abc:false next 3+0"},
		{textPosition{Page: 2, Offset: 1}, 0, "2:bc:true 3:xyz:false"},
		{textPosition{Page: 3}, 3, "3:xyz:false"},
		{textPosition{Page: 9}, 3, ""},
	} {
		if got := render(textRange(pages, c.from, c.limit)); got != c.want {
			t.Errorf("from %+v limit %d: %q, want %q", c.from, c.limit, got, c.want)
		}
	}
}
