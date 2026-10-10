package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/dms"
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

func TestHTTPGroupPermissionsAndModuleScopeRevocation(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	d := s.Directory()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Tasks"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Editors"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", g.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	body := `{"inherit":false,"grants":[{"principal_type":"group","principal_id":"` + g.ID + `","action":"write"}]}`
	resp := request(h, "PUT", "/v1/resources/"+list.ID+"/permissions", body, testToken, `"1"`, "application/json")
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Draft"})
	if err != nil {
		t.Fatal(err)
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	resp = request(bob, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = request(bob, "POST", "/v1/identity/groups", `{"name":"Unauthorized"}`, testToken, "", "application/json")
	if resp.Code != 403 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	if err = d.SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	resp = request(bob, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	if resp.Code != 403 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = request(bob, "GET", "/v1/identity/me/scopes", "", testToken, "", "")
	if resp.Code != 200 || string(resp.Body.Bytes()) != "[]\n" {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = request(h, "PUT", "/v1/resources/"+list.ID+"/permissions", `{"grants":[{"subject":"alice","action":"manage"}]}`, testToken, `"2"`, "application/json")
	if resp.Code != 400 {
		t.Fatal(resp.Code, resp.Body.String())
	}
}
