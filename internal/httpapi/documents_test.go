package httpapi

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"papergo/ent"
	"papergo/internal/dms"
)

func TestRestoreFileVersionAPI(t *testing.T) {
	h, s, path := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/items/" + item.ID + "/content"
	res := request(h, "PUT", endpoint, "old content", testToken, `"1"`, "text/plain")
	var original ent.Blob
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &original) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	if _, err = s.Publish(ctx, "alice", item.ID, 2); err != nil {
		t.Fatal(err)
	}
	res = request(h, "PUT", endpoint, "new content", testToken, `"3"`, "text/plain")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	name := "Current metadata"
	item, err = s.Update(ctx, "alice", item.ID, 4, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	restore := endpoint + "/versions/1/restore"
	for _, check := range []struct {
		match  string
		status int
	}{{"", 428}, {`"4"`, 409}} {
		res = request(h, "POST", restore, "", testToken, check.match, "")
		if res.Code != check.status {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreBlob(ctx, "reader", item.ID, 1, item.Version); !errors.Is(err, dms.ErrForbidden) {
		t.Fatal("reader restored file", err)
	}
	res = request(h, "POST", endpoint+"/versions/99/restore", "", testToken, `"5"`, "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", restore, "", testToken, `"5"`, "")
	var restored ent.Blob
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &restored) != nil || restored.Version != 3 || restored.ID == original.ID || restored.Sha256 != original.Sha256 || res.Header().Get("ETag") != `"6"` {
		t.Fatal(res.Code, res.Body.String())
	}
	item, err = s.Get(ctx, "alice", item.ID)
	if err != nil || item.Name != name || item.Version != 6 {
		t.Fatal(item, err)
	}
	published, err := s.Get(ctx, "reader", item.ID)
	if err != nil || *published.PublishedRevisionID == *item.HeadRevisionID {
		t.Fatal("restore unexpectedly published", published, err)
	}
	res = request(h, "GET", endpoint, "", testToken, "", "")
	if res.Code != 200 || res.Body.String() != "old content" {
		t.Fatal(res.Code, res.Body.String())
	}
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 2 {
		t.Fatal("restore copied or removed objects", len(files), err)
	}
	// Purge queues each shared object once and removes it only after all of the
	// item's immutable versions have been removed.
	if err = s.Delete(ctx, "alice", item.ID, item.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	res = request(h, "DELETE", "/v1/resources/"+lib.ID+"/recycle-bin/"+item.ID, "", testToken, "", "")
	if res.Code != 204 {
		t.Fatal(res.Code, res.Body.String())
	}
	// The purge endpoint performs storage cleanup immediately.
	files, err = os.ReadDir(path)
	if err != nil || len(files) != 0 {
		t.Fatal("purge left restored bytes", len(files), err, strconv.Itoa(item.Version))
	}
}

func TestDocumentDuplicatePolicyAPI(t *testing.T) {
	h, s, path := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	first := "/v1/items/" + a.ID + "/content"
	second := "/v1/items/" + b.ID + "/content"
	res := request(h, "PUT", first, "same bytes", testToken, `"1"`, "text/plain")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PUT", second, "same bytes", testToken, `"1"`, "text/plain")
	var upload struct {
		Duplicates []dms.DuplicateDocument `json:"duplicates"`
	}
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &upload) != nil || len(upload.Duplicates) != 1 || upload.Duplicates[0].ItemID != a.ID {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", "/v1/resources/"+lib.ID, `{"duplicate_policy":"block"}`, testToken, `"1"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PUT", second, "same bytes", testToken, `"2"`, "text/plain")
	if res.Code != 409 || !strings.Contains(res.Body.String(), `"code":"duplicate_file"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 2 {
		t.Fatal("rejected duplicate left an object", len(files), err)
	}
	res = request(h, "PUT", second, "new bytes", testToken, `"2"`, "text/plain")
	if res.Code != 201 {
		t.Fatal("failed upload consumed version", res.Code, res.Body.String())
	}
	// Historical bytes in B no longer block a replacement of A by itself.
	res = request(h, "PUT", first, "same bytes", testToken, `"2"`, "text/plain")
	if res.Code != 201 {
		t.Fatal("self/historical duplicate", res.Code, res.Body.String())
	}
	res = request(h, "PATCH", "/v1/resources/"+w.ID, `{"duplicate_policy":"allow"}`, testToken, `"1"`, "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", "/v1/resources/"+lib.ID, `{"duplicate_policy":"invalid"}`, testToken, `"2"`, "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
}
