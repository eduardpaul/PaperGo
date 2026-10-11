package httpapi

import (
	"fmt"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestMetadataEditorCannotReplaceFileWithoutDocumentWrite(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "File"})
	if err != nil {
		t.Fatal(err)
	}
	d := s.Directory()
	role, err := d.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Metadata editor", Scopes: []string{"workspace.read", "list.read", "list.write", "document.read"}})
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{Type: "user", ID: "alice"}
	if err = d.SetAssignment(ctx, "organization-admin", role.ID, principal, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "organization-admin", identity.MemberRole, principal, true); err != nil {
		t.Fatal(err)
	}
	response := request(h, "PATCH", "/v1/resources/"+item.ID, `{"name":"Edited metadata"}`, testToken, fmt.Sprintf(`"%d"`, item.Version), "application/json")
	if response.Code != 200 {
		t.Fatal("metadata edit denied", response.Code, response.Body.String())
	}
	response = request(h, "PUT", "/v1/items/"+item.ID+"/content", "%PDF-replacement", testToken, response.Header().Get("ETag"), "text/plain")
	if response.Code != 403 {
		t.Fatal("file replacement accepted without document.write", response.Code, response.Body.String())
	}
	response = request(h, "POST", "/v1/resources/"+lib.ID+"/documents", "", testToken, "", "multipart/form-data")
	if response.Code != 403 {
		t.Fatal("document creation accepted without document.write", response.Code, response.Body.String())
	}
}
