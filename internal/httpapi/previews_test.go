package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/storage"
)

func TestPreviewEndpointUsesAuthorizedVisibleBlobAndNeverReturnsOriginal(t *testing.T) {
	h, s, path := setup(t)
	ctx := context.Background()
	store, err := storage.NewLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Previews"})
	if err != nil {
		t.Fatal(err)
	}
	library, err := s.Create(ctx, "alice", workspace.ID, dms.CreateResource{Kind: "library", Name: "Files", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", library.ID, dms.CreateResource{Kind: "item", Name: "Document"})
	if err != nil {
		t.Fatal(err)
	}
	file := request(h, "PUT", "/v1/items/"+item.ID+"/content", "%PDF-old", testToken, `"1"`, "application/pdf")
	if file.Code != 201 {
		t.Fatal(file.Code, file.Body.String())
	}
	old, err := s.GetBlob(ctx, "alice", item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/items/" + item.ID + "/preview"
	pending := request(h, http.MethodGet, endpoint, "", testToken, "", "")
	if pending.Code != 202 || strings.Contains(pending.Body.String(), "%PDF") {
		t.Fatal(pending.Code, pending.Body.String())
	}
	rendition, err := store.Put(ctx, strings.NewReader("tiny-webp-preview"), 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePreview(ctx, old.Sha256, rendition, 10, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Publish(ctx, "alice", item.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", workspace.ID, dms.WorkspaceMember{Subject: "reader", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	reader := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "reader"}, Storage: store, Logger: slog.Default()}).Handler()
	newer := request(h, "PUT", "/v1/items/"+item.ID+"/content", "%PDF-new", testToken, `"3"`, "application/pdf")
	if newer.Code != 201 {
		t.Fatal(newer.Code, newer.Body.String())
	}
	head, err := s.GetBlob(ctx, "alice", item.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	visible := request(reader, "GET", endpoint, "", testToken, "", "")
	if visible.Code != 200 || visible.Body.String() != "tiny-webp-preview" || visible.Header().Get("X-Preview-Blob-ID") != old.ID || visible.Header().Get("Content-Type") != "image/webp" {
		t.Fatal(visible.Code, visible.Body.String(), visible.Header())
	}
	if got := request(reader, "GET", endpoint+"?blob_id="+head.ID, "", testToken, "", ""); got.Code != 404 {
		t.Fatal("draft preview leaked", got.Code)
	}
	if got := request(h, "GET", endpoint, "", testToken, "", ""); got.Code != 202 {
		t.Fatal("head borrowed old preview", got.Code)
	}
	if err = s.SavePreview(ctx, head.Sha256, storage.Object{}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if got := request(h, "GET", endpoint, "", testToken, "", ""); got.Code != 200 || !strings.Contains(got.Body.String(), "unavailable") {
		t.Fatal(got.Code, got.Body.String())
	}
	current, err := s.Get(ctx, "alice", item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetPermissions(ctx, "alice", item.ID, current.Version, dms.Permissions{Grants: []dms.Permission{{PrincipalType: "user", PrincipalID: "alice", Action: "manage", Effect: "allow"}}}); err != nil {
		t.Fatal(err)
	}
	if got := request(reader, "GET", endpoint, "", testToken, "", ""); got.Code != 403 {
		t.Fatal("private rendition leaked", got.Code, got.Body.String())
	}
}
