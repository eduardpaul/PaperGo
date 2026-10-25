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

func TestUploadsStoreAnyFileAndNameItsType(t *testing.T) {
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
	// The filename has a .txt suffix and the declared type is JPEG, but the
	// bytes are a PDF.
	response := request(h, "PUT", endpoint, "%PDF-1.7", testToken, `"1"`, "image/jpeg")
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"content_type":"application/pdf"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	// Bytes no type describes are stored as they are.
	response = request(h, "PUT", endpoint, "plain text", testToken, `"2"`, "application/pdf")
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"content_type":"application/octet-stream"`) {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "GET", endpoint, "", testToken, "", "")
	if response.Code != 200 || response.Header().Get("Content-Type") != "application/octet-stream" || response.Body.String() != "plain text" ||
		response.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatal(files, err)
	}
	for name, want := range map[string]string{"page.html": "text/html", "fake.pdf": "application/octet-stream", "rows.csv": "text/csv"} {
		response = documentRequest(t, h, "/v1/resources/"+l.ID+"/documents", documentPart{"file", name, "<p>plain text</p>"})
		if response.Code != 201 || !strings.Contains(response.Body.String(), `"content_type":"`+want+`"`) {
			t.Fatal(name, response.Code, response.Body.String())
		}
	}
	if count, _ := s.DAL.Resources().Count(ctx, resources.Filter{}); count != 6 {
		t.Fatal("documents not created", count)
	}
	if !transferRequest(httptest.NewRequest(http.MethodPut, endpoint, nil)) {
		t.Fatal("upload not classified as transfer")
	}
}
