package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"

	"papergo/ent"
	"papergo/ent/collectiontype"
	"papergo/ent/itemrevision"
	"papergo/internal/auth"
	"papergo/internal/dms"
)

type documentPart struct{ name, filename, body string }

type unreadDocumentBody struct{ read bool }

func (b *unreadDocumentBody) Read(p []byte) (int, error) {
	b.read = true
	return 0, errors.New("body must not be read")
}

func documentRequest(t *testing.T, h http.Handler, path string, parts ...documentPart) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, p := range parts {
		header := textproto.MIMEHeader{}
		params := map[string]string{"name": p.name}
		if p.filename != "" {
			params["filename"] = p.filename
		}
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", params))
		if p.filename != "" {
			header.Set("Content-Type", "text/plain")
		}
		part, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte(p.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return request(h, "POST", path, body.String(), testToken, "", writer.FormDataContentType())
}

func TestDocumentMultipartCreateAtomicMetadataAndContent(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Files", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "folder", Name: "Inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "amount", Label: "Amount", Type: "integer", Required: true}); err != nil {
		t.Fatal(err)
	}
	typ, err := s.Client.CollectionType.Query().Where(collectiontype.ContainerIDEQ(lib.ID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	metadata := `{"name":"Invoice","folder_id":"` + folder.ID + `","content_type_id":"` + typ.ContentTypeID + `","tags":["finance"],"values":{"amount":9223372036854775807},"languages":" eng+deu "}`
	endpoint := "/v1/resources/" + lib.ID + "/documents"
	// Metadata may follow the file; no item is created until the full multipart
	// body has been parsed and validated.
	res := documentRequest(t, h, endpoint, documentPart{"file", `C:\inbox\invoice.txt`, "%PDF-invoice bytes"}, documentPart{"metadata", "", metadata})
	var result struct {
		Item       ent.Resource            `json:"item"`
		File       ent.Blob                `json:"file"`
		Duplicates []dms.DuplicateDocument `json:"duplicates"`
	}
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &result) != nil || result.Item.Name != "Invoice" || result.File.Filename != "invoice.txt" || result.File.Languages != "eng+deu" || *result.Item.ParentID != folder.ID || *result.Item.ContentTypeID != typ.ContentTypeID || result.Item.Version != 1 || result.File.Version != 1 || res.Header().Get("ETag") != `"1"` || !strings.Contains(res.Body.String(), "9223372036854775807") {
		t.Fatal(res.Code, res.Body.String())
	}
	if result.Item.PublishedRevisionID != nil || res.Header().Get("Location") != "/v1/resources/"+result.Item.ID {
		t.Fatal("first upload publication", res.Body.String())
	}
	if n, err := s.Client.ItemRevision.Query().Where(itemrevision.ItemIDEQ(result.Item.ID)).Count(ctx); err != nil || n != 1 {
		t.Fatal("metadata-only intermediate revision", n, err)
	}
	res = request(h, "GET", "/v1/items/"+result.Item.ID+"/content", "", testToken, "", "")
	if res.Code != 200 || res.Body.String() != "%PDF-invoice bytes" {
		t.Fatal(res.Code, res.Body.String())
	}
	// A second multipart upload reports the same warnings as replacement.
	metadata = strings.Replace(metadata, `"Invoice"`, `"Copy"`, 1)
	res = documentRequest(t, h, endpoint, documentPart{"metadata", "", metadata}, documentPart{"file", "copy.txt", "%PDF-invoice bytes"})
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &result) != nil || len(result.Duplicates) != 1 {
		t.Fatal(res.Code, res.Body.String())
	}
}

func TestDocumentMultipartFailuresLeaveNoItemOrBytes(t *testing.T) {
	h, s, path := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	policy := "block"
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Files", DuplicatePolicy: &policy})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", lib.ID, dms.CreateField{Key: "required", Label: "Required", Type: "text", Required: true}); err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/resources/" + lib.ID + "/documents"
	file := documentPart{"file", "file.txt", "%PDF-bytes"}
	valid := documentPart{"metadata", "", `{"values":{"required":"yes"}}`}
	before, err := s.Client.Resource.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		parts  []documentPart
		status int
	}{
		{"missing file", []documentPart{valid}, 400},
		{"required metadata", []documentPart{file}, 422},
		{"unknown metadata", []documentPart{file, {"metadata", "", `{"unknown":true}`}}, 400},
		{"null metadata", []documentPart{file, {"metadata", "", "null"}}, 400},
		{"trailing metadata", []documentPart{file, {"metadata", "", `{} {}`}}, 400},
		{"two files", []documentPart{file, file}, 400},
		{"unknown part", []documentPart{file, {"other", "", "value"}}, 400},
		{"too large file", []documentPart{{"file", "file.txt", strings.Repeat("x", 33)}, valid}, 413},
		{"too large metadata", []documentPart{file, {"metadata", "", strings.Repeat("x", int(documentMetadataLimit)+1)}}, 413},
		{"bad languages", []documentPart{file, {"metadata", "", `{"values":{"required":"yes"},"languages":"ENG"}`}}, 422},
		{"bad folder", []documentPart{file, {"metadata", "", `{"values":{"required":"yes"},"folder_id":"` + w.ID + `"}`}}, 422},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := documentRequest(t, h, endpoint, test.parts...)
			if res.Code != test.status {
				t.Fatal(res.Code, res.Body.String())
			}
			if n, err := s.Client.Resource.Query().Count(ctx); err != nil || n != before {
				t.Fatal("failure created item", n, err)
			}
			files, err := os.ReadDir(path)
			if err != nil || len(files) != 0 {
				t.Fatal("failure left bytes", len(files), err)
			}
		})
	}
	res := documentRequest(t, h, endpoint, file, valid)
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	// Different names do not bypass byte identity under Block policy.
	res = documentRequest(t, h, endpoint, documentPart{"file", "another.txt", "%PDF-bytes"}, valid)
	if res.Code != 409 {
		t.Fatal(res.Code, res.Body.String())
	}
	if n, err := s.Client.Resource.Query().Count(ctx); err != nil || n != before+1 {
		t.Fatal("duplicate created item", n, err)
	}
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 1 {
		t.Fatal("duplicate left bytes", len(files), err)
	}
}

func TestDocumentMultipartAuthorizationBeforeReading(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "No uploads"})
	if err != nil {
		t.Fatal(err)
	}
	res := documentRequest(t, h, "/v1/resources/"+w.ID+"/documents", documentPart{"file", "f", "%PDF-bytes"})
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	private := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "outsider"}, Logger: slog.Default(), MaxUpload: 32}).Handler()
	body := &unreadDocumentBody{}
	r := httptest.NewRequest("POST", "/v1/resources/"+lib.ID+"/documents", body)
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	response := httptest.NewRecorder()
	private.ServeHTTP(response, r)
	if response.Code != 403 || body.read {
		t.Fatal("unauthorized upload read bytes", response.Code, body.read, response.Body.String())
	}
	// Streaming classification applies only to multipart creation's route.
	if !transferRequest(httptest.NewRequest("POST", "/v1/resources/id/documents", nil)) || transferRequest(httptest.NewRequest("POST", "/v1/items/id/content", nil)) {
		t.Fatal("document transfer classification")
	}
}

func TestDocumentMultipartDatabaseFailureRollsBackAndCleansStorage(t *testing.T) {
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
	before, err := s.Client.Resource.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Client.ExecContext(ctx, `CREATE TRIGGER reject_document_blob BEFORE INSERT ON blobs BEGIN SELECT RAISE(ABORT,'injected blob failure'); END`); err != nil {
		t.Fatal(err)
	}
	res := documentRequest(t, h, "/v1/resources/"+lib.ID+"/documents", documentPart{"file", "doc.txt", "%PDF-bytes"})
	if res.Code != 409 {
		t.Fatal(res.Code, res.Body.String())
	}
	if n, err := s.Client.Resource.Query().Count(ctx); err != nil || n != before {
		t.Fatal("database failure left metadata", n, err)
	}
	if n, err := s.Client.Blob.Query().Count(ctx); err != nil || n != 0 {
		t.Fatal("database failure left blob", n, err)
	}
	files, err := os.ReadDir(path)
	if err != nil || len(files) != 0 {
		t.Fatal("database failure left bytes", len(files), err)
	}
}

func TestUploadNameConflictAndRenameDownload(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Files"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "library", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/v1/resources/" + lib.ID + "/documents"
	first := documentRequest(t, h, endpoint, documentPart{"file", "original.pdf", "%PDF-first"})
	var doc struct {
		Item ent.Resource `json:"item"`
	}
	if first.Code != 201 || json.Unmarshal(first.Body.Bytes(), &doc) != nil {
		t.Fatal(first.Code, first.Body.String())
	}
	conflict := documentRequest(t, h, endpoint, documentPart{"file", "ORIGINAL.pdf", "%PDF-second"})
	var problem struct {
		Code     string                   `json:"code"`
		Existing dms.DocumentNameConflict `json:"existing_item"`
	}
	if conflict.Code != 409 || json.Unmarshal(conflict.Body.Bytes(), &problem) != nil || problem.Code != "name_conflict" || problem.Existing.ItemID != doc.Item.ID || problem.Existing.Version != 1 {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
	renamed := request(h, "PATCH", "/v1/resources/"+doc.Item.ID, `{"name":"Quarterly"}`, testToken, `"1"`, "application/json")
	if renamed.Code != 200 {
		t.Fatal(renamed.Code, renamed.Body.String())
	}
	download := request(h, "GET", "/v1/items/"+doc.Item.ID+"/content", "", testToken, "", "")
	if download.Code != 200 || !strings.Contains(download.Header().Get("Content-Disposition"), "Quarterly.pdf") || download.Body.String() != "%PDF-first" {
		t.Fatal(download.Code, download.Header(), download.Body.String())
	}
}
