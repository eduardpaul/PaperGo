package httpapi

import (
	"testing"

	directorydomain "papergo/internal/dal/directory"
	"papergo/internal/identity"
)

func TestTrustedHostRoutes(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	call := func(method, path, body, match string, want int) string {
		t.Helper()
		w := request(h, method, path, body, testToken, match, "application/json")
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	const path = "/v1/organization/settings/trusted-hosts"
	if body := call("GET", path, "", "", 200); body != `{"hosts":["*"],"version":0}`+"\n" {
		t.Fatalf("a new organization: %s", body)
	}
	call("PUT", path, `{"hosts": ["docs.example.com"]}`, `"0"`, 403)
	call("GET", path+"/usage", "", "", 403)
	directory := s.Directory()
	role, err := directory.SaveRole(ctx, "organization-admin", "", 0, identity.RoleInput{Name: "Organization", Scopes: []string{"organization.manage"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetAssignment(ctx, "organization-admin", role.ID, identity.Principal{Type: directorydomain.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	call("PUT", path, `{"hosts": ["docs.example.com"]}`, "", 428)
	call("PUT", path, `{}`, `"0"`, 422)
	call("PUT", path, `{"hosts": ["https://docs.example.com"]}`, `"0"`, 422)
	if body := call("PUT", path, `{"hosts": ["Docs.Example.com", "*.example.org"]}`, `"0"`, 200); body != `{"hosts":["docs.example.com","*.example.org"],"version":1}`+"\n" {
		t.Fatalf("saved: %s", body)
	}
	call("PUT", path, `{"hosts": ["*"]}`, `"0"`, 412)
	if body := call("GET", path+"/usage", "", "", 200); body != `{"value":[]}`+"\n" {
		t.Fatalf("usage: %s", body)
	}
}
