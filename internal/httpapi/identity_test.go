package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/dal/directory"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
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
	if err := s.Directory().SetAssignment(context.Background(), "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "organization-admin"}, true); err != identity.ErrLastAdministrator {
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
	if err = d.SetMember(ctx, "alice", g.ID, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, false); err != nil {
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
	if err = d.SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	resp = request(bob, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	if resp.Code != 403 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = request(bob, "GET", "/v1/identity/me/scopes", "", testToken, "", "")
	if resp.Code != 200 || resp.Body.String() != "[]\n" {
		t.Fatal(resp.Code, resp.Body.String())
	}
	resp = request(h, "PUT", "/v1/resources/"+list.ID+"/permissions", `{"grants":[{"subject":"alice","action":"manage"}]}`, testToken, `"2"`, "application/json")
	if resp.Code != 400 {
		t.Fatal(resp.Code, resp.Body.String())
	}
}

func TestIdentityMeEndpoint(t *testing.T) {
	_, s, _ := setup(t)
	bobAuth := auth.Development{
		Token:             testToken,
		Subject:           "bob",
		Name:              "Bob Builder",
		PreferredUsername: "bob",
		Email:             "bob@example.com",
		Groups:            []string{"developers", "contractors"},
	}
	bobAPI := (&API{DMS: s, Auth: bobAuth, Logger: slog.Default()}).Handler()
	resp := request(bobAPI, "GET", "/v1/identity/me", "", testToken, "", "")
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var me identity.MeProfile
	if err := json.Unmarshal(resp.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Subject != "bob" {
		t.Fatalf("expected subject bob, got %s", me.Subject)
	}
	if me.DisplayName != "Bob Builder" || me.Email != "bob@example.com" {
		t.Fatalf("unexpected profile: %+v", me)
	}
	if len(me.Groups) != 2 || me.Groups[0] != "contractors" || me.Groups[1] != "developers" {
		t.Fatalf("unexpected groups: %+v", me.Groups)
	}
	if len(me.Scopes) == 0 {
		t.Fatal("expected scopes to be populated")
	}
}

func TestIdentityUsersAndPeopleEndpointsReturnProfiles(t *testing.T) {
	h, s, _ := setup(t)
	// Bob signs in via custom dev auth to sync profile
	bobAuth := auth.Development{
		Token:             testToken,
		Subject:           "bob",
		Name:              "Bob Builder",
		PreferredUsername: "bob",
		Email:             "bob@example.com",
	}
	bobAPI := (&API{DMS: s, Auth: bobAuth, Logger: slog.Default()}).Handler()
	_ = request(bobAPI, "GET", "/v1/identity/me", "", testToken, "", "")

	// Organization admin reads GET /v1/identity/users
	resp := request(h, "GET", "/v1/identity/users", "", testToken, "", "")
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var users []identity.UserProfile
	if err := json.Unmarshal(resp.Body.Bytes(), &users); err != nil {
		t.Fatal(err)
	}
	var bobFound *identity.UserProfile
	for i := range users {
		if users[i].Subject == "bob" {
			bobFound = &users[i]
			break
		}
	}
	if bobFound == nil || bobFound.DisplayName != "Bob Builder" || bobFound.Email != "bob@example.com" {
		t.Fatalf("unexpected bob in users: %+v", bobFound)
	}

	// Organization admin reads GET /v1/people
	resp = request(h, "GET", "/v1/people", "", testToken, "", "")
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	var people []string
	if err := json.Unmarshal(resp.Body.Bytes(), &people); err != nil {
		t.Fatal(err)
	}
	var foundBob bool
	for _, id := range people {
		if id == "bob" {
			foundBob = true
			break
		}
	}
	if !foundBob {
		t.Fatalf("expected bob in people: %+v", people)
	}
}

func TestOIDCGroupMemberAPIReturnsConflict(t *testing.T) {
	h, s, _ := setup(t)
	// Sync user carol with an oidc group
	carolAuth := auth.Development{
		Token:   testToken,
		Subject: "carol",
		Groups:  []string{"oidc-admins"},
	}
	carolAPI := (&API{DMS: s, Auth: carolAuth, Logger: slog.Default()}).Handler()
	_ = request(carolAPI, "GET", "/v1/identity/me", "", testToken, "", "")

	// List groups to find oidc-admins ID
	resp := request(h, "GET", "/v1/identity/groups", "", testToken, "", "")
	if resp.Code != 200 {
		t.Fatal(resp.Code, resp.Body.String())
	}
	var groups []identity.Group
	if err := json.Unmarshal(resp.Body.Bytes(), &groups); err != nil {
		t.Fatal(err)
	}
	var oidcGrpID string
	for _, g := range groups {
		if g.Name == "oidc-admins" {
			oidcGrpID = g.ID
			if g.Source != "oidc" {
				t.Fatalf("expected source oidc, got %s", g.Source)
			}
			break
		}
	}
	if oidcGrpID == "" {
		t.Fatal("oidc-admins group not found")
	}

	// Attempt PUT /v1/identity/groups/{id}/members/user/bob -> should fail with 409
	resp = request(h, "PUT", "/v1/identity/groups/"+oidcGrpID+"/members/user/bob", "", testToken, "", "")
	if resp.Code != 409 {
		t.Fatalf("expected 409 for member add to oidc group, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "group_managed_by_identity_provider") {
		t.Fatalf("expected group_managed_by_identity_provider problem code, got %s", resp.Body.String())
	}

	// Attempt DELETE /v1/identity/groups/{id}/members/user/carol -> should fail with 409
	resp = request(h, "DELETE", "/v1/identity/groups/"+oidcGrpID+"/members/user/carol", "", testToken, "", "")
	if resp.Code != 409 {
		t.Fatalf("expected 409 for member remove from oidc group, got %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "group_managed_by_identity_provider") {
		t.Fatalf("expected group_managed_by_identity_provider problem code, got %s", resp.Body.String())
	}
}
