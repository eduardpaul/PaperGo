package httpapi

import (
	"context"
	"encoding/json"
	"papergo/internal/identity"
	"testing"
)

func TestIdentityAPIUsesExplicitPrincipalsAndVersions(t *testing.T) {
	h, s, _ := setup(t)
	w := request(h, "POST", "/v1/identity/groups", `{"name":"Editors"}`, testToken, "", "application/json")
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatal(w.Code, w.Body.String())
	}
	var g identity.Group
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	base := "/v1/identity/groups/" + g.ID
	w = request(h, "PUT", base, `{"name":"Changed"}`, testToken, "", "application/json")
	if w.Code != 428 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "PUT", base+"/members/user/bob", "", testToken, "", "")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", base+"/members", "", testToken, "", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var members []identity.Principal
	if err := json.Unmarshal(w.Body.Bytes(), &members); err != nil || len(members) != 1 || members[0].Type != "user" || members[0].ID != "bob" {
		t.Fatal(w.Body.String(), err)
	}
	w = request(h, "PUT", base+"/members/group/"+g.ID, "", testToken, "", "")
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := s.Directory().SetAssignment(context.Background(), "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "organization-admin"}, true); err != identity.ErrLastAdministrator {
		t.Fatal(err)
	}
}
