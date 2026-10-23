package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"papergo/internal/dal/blobs"
	"papergo/internal/dal/directory"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
	"testing"
)

type changingInboxBody struct {
	io.Reader
	change func()
}

func (b *changingInboxBody) Read(p []byte) (int, error) {
	if b.change != nil {
		f := b.change
		b.change = nil
		f()
	}
	return b.Reader.Read(p)
}

func TestInboxUploadRechecksChangesDuringStreaming(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
	}{{"membership", 404}, {"binding", 409}, {"active", 403}, {"credential", 403}} {
		t.Run(scenario.name, func(t *testing.T) {
			h, s, _ := setup(t)
			ctx := t.Context()
			home, err := s.Home(ctx, "alice")
			if err != nil {
				t.Fatal(err)
			}
			group, err := s.Directory().SaveGroup(ctx, "organization-admin", "", 0, identity.GroupInput{Name: "Streaming"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Directory().SetMember(ctx, "organization-admin", group.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
				t.Fatal(err)
			}
			if _, err = s.SetGroupInbox(ctx, "alice", group.ID, dms.GroupInboxInput{LibraryID: home.InboxLibraryID}); err != nil {
				t.Fatal(err)
			}
			authToken := testToken
			var token identity.CreatedAccessToken
			if scenario.name == "credential" {
				token, err = s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Upload", Scopes: []string{"document.write", "item.read", "item.write"}})
				if err != nil {
					t.Fatal(err)
				}
				authToken = token.Secret
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "incoming.pdf")
			if err != nil {
				t.Fatal(err)
			}
			part.Write([]byte("%PDF-streaming"))
			writer.Close()
			change := func() {
				var err error
				switch scenario.name {
				case "membership":
					err = s.Directory().SetMember(ctx, "organization-admin", group.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, true)
				case "binding":
					_, err = s.SetGroupInbox(ctx, "alice", group.ID, dms.GroupInboxInput{LibraryID: home.DocumentsLibraryID})
				case "active":
					_, err = s.Directory().SetUserActive(ctx, "organization-admin", "alice", false)
				case "credential":
					err = s.Directory().RevokeAccessToken(ctx, "alice", token.Token.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/v1/identity/groups/"+group.ID+"/inbox/documents", &changingInboxBody{Reader: bytes.NewReader(body.Bytes()), change: change})
			req.Header.Set("Authorization", "Bearer "+authToken)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != scenario.status {
				t.Fatal(res.Code, res.Body.String())
			}
			if n, err := s.DAL.Blobs().Count(ctx, blobs.BlobFilter{}); err != nil || n != 0 {
				t.Fatal("failed upload attached bytes", n, err)
			}
		})
	}
}

func TestHomeAndInboxHTTPContracts(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	res := request(h, "GET", "/v1/identity/me/home", "", testToken, "", "")
	var home dms.Home
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &home) != nil || home.WorkspaceID == "" {
		t.Fatal(res.Code, res.Body.String())
	}
	repeated := request(h, "GET", "/v1/identity/me/home", "", testToken, "", "")
	if repeated.Code != 200 || repeated.Body.String() != res.Body.String() {
		t.Fatal("unstable discovery", repeated.Code, repeated.Body.String())
	}
	res = documentRequest(t, h, "/v1/identity/me/inbox/documents", documentPart{"file", "upload.pdf", "%PDF-personal"}, documentPart{"metadata", "", `{"name":"Incoming","values":{"description":"Triage"},"languages":"eng"}`})
	var upload struct {
		Item resources.Resource `json:"item"`
		File blobs.Blob         `json:"file"`
	}
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &upload) != nil || upload.Item.ContainerID == nil || *upload.Item.ContainerID != home.InboxLibraryID || upload.File.Languages != "eng" || res.Header().Get("ETag") == "" || res.Header().Get("Location") == "" {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, field := range []string{"folder_id", "content_type_id"} {
		res = documentRequest(t, h, "/v1/identity/me/inbox/documents", documentPart{"file", "bad.pdf", "%PDF-other"}, documentPart{"metadata", "", `{"` + field + `":"` + home.InboxLibraryID + `"}`})
		if res.Code != 422 {
			t.Fatal("override accepted", field, res.Code, res.Body.String())
		}
		for _, empty := range []string{"null", "\"\""} {
			res = documentRequest(t, h, "/v1/identity/me/inbox/documents", documentPart{"file", "empty.pdf", "%PDF-other"}, documentPart{"metadata", "", `{"` + field + `":` + empty + `}`})
			if res.Code != 422 {
				t.Fatal("empty override accepted", field, res.Code, res.Body.String())
			}
		}
	}
	res = request(h, "DELETE", "/v1/resources/"+home.InboxLibraryID, "", testToken, `"1"`, "")
	if res.Code != 409 || !strings.Contains(res.Body.String(), "protected_home") {
		t.Fatal(res.Code, res.Body.String())
	}
	group, err := s.Directory().SaveGroup(ctx, "organization-admin", "", 0, identity.GroupInput{Name: "Intake"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/identity/groups/" + group.ID + "/inbox"
	res = request(h, "PUT", path, `{"library_id":"`+home.DocumentsLibraryID+`"}`, testToken, "", "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = documentRequest(t, h, path+"/documents", documentPart{"file", "group.pdf", "%PDF-group"})
	if res.Code != 404 {
		t.Fatal("nonmember upload", res.Code, res.Body.String())
	}
	if err = s.Directory().SetMember(ctx, "organization-admin", group.ID, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	res = documentRequest(t, h, path+"/documents", documentPart{"file", "group.pdf", "%PDF-group"})
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &upload) != nil || *upload.Item.ContainerID != home.DocumentsLibraryID {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/identity/me/inboxes", "", testToken, "", "")
	var inboxes []dms.Inbox
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &inboxes) != nil || len(inboxes) != 2 || inboxes[0].GroupID != nil || *inboxes[1].GroupID != group.ID {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "DELETE", path, "", testToken, "", "")
	if res.Code != 204 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/items/"+upload.Item.ID+"/content", "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal("unbind removed document", res.Code, res.Body.String())
	}
}

func TestInboxUploadRejectsMissingScopeBeforeReadingBody(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	token, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "File only", Scopes: []string{"document.write", "item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	body := &unreadDocumentBody{}
	req := httptest.NewRequest(http.MethodPost, "/v1/identity/me/inbox/documents", body)
	req.Header.Set("Authorization", "Bearer "+token.Secret)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != 403 || body.read {
		t.Fatal("body read before authorization", res.Code, body.read, res.Body.String())
	}
	for _, path := range []string{"/v1/identity/me/inbox/documents", "/v1/identity/groups/abc/inbox/documents"} {
		if !transferRequest(httptest.NewRequest(http.MethodPost, path, nil)) {
			t.Fatal("missing transfer classification", path)
		}
	}
}
