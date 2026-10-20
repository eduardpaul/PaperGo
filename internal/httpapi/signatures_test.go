package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestUploadUsesSignatureAndRejectsUnsupportedBytes(t *testing.T) {
	h, s, dir := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Signatures"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "File"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/items/" + item.ID + "/content"
	response := request(h, "PUT", endpoint, "%PDF-1.7", testToken, `"1"`, "image/jpeg")
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"content_type":"application/pdf"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	// The filename has a .txt suffix and the declared type was JPEG, but the
	// signature is PDF. Unsupported replacements do not change the head.
	response = request(h, "PUT", endpoint, "plain text", testToken, `"2"`, "application/pdf")
	if response.Code != 415 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "GET", endpoint, "", testToken, "", "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/pdf" || response.Body.String() != "%PDF-1.7" {
		t.Fatal(response.Code, response.Body.String())
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	response = documentRequest(t, h, "/v1/resources/"+l.ID+"/documents", documentPart{"file", "fake.pdf", "plain text"})
	if response.Code != 415 {
		t.Fatal(response.Code, response.Body.String())
	}
	if count, _ := s.DAL.Resources().Count(ctx, resources.Filter{}); count != 3 {
		t.Fatal("unsupported creation wrote metadata", count)
	}
	if !transferRequest(httptest.NewRequest(http.MethodPut, endpoint, nil)) {
		t.Fatal("upload not classified as transfer")
	}
}
