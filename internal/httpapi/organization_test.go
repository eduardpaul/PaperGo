package httpapi

import (
	"encoding/json"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/identity"
	"testing"
)

func TestRESTOrganizationIdentityAndProfile(t *testing.T) {
	h, s, _ := setup(t)
	r := request(h, "GET", "/v1/organization", "", testToken, "", "")
	var org identity.Organization
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &org) != nil || org.ID == "" || org.Identifier != "papergo" || org.DisplayName != "PaperGo" || r.Header().Get("ETag") != `"1"` {
		t.Fatal(r.Code, r.Body.String())
	}
	admin := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "organization-admin"}, Logger: slog.Default()}).Handler()
	r = request(h, "PATCH", "/v1/organization", `{"display_name":"Forbidden"}`, testToken, `"1"`, "application/json")
	if r.Code != 403 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(admin, "PATCH", "/v1/organization", `{"identifier":"acme","display_name":"Acme","description":"Core organization"}`, testToken, `"1"`, "application/json")
	if r.Code != 200 || r.Header().Get("ETag") != `"2"` {
		t.Fatal(r.Code, r.Body.String())
	}
	var updated identity.Organization
	if err := json.Unmarshal(r.Body.Bytes(), &updated); err != nil || updated.ID != org.ID || updated.Identifier != "acme" {
		t.Fatal(updated, err)
	}
	r = request(admin, "PATCH", "/v1/organization", `{"display_name":"Stale"}`, testToken, `"1"`, "application/json")
	if r.Code != 412 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/me", "", testToken, "", "")
	var me identity.MeProfile
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &me) != nil || me.OrganizationID != org.ID || me.OrganizationIdentifier != "acme" {
		t.Fatal(r.Code, r.Body.String())
	}
	// Initializing the directory again never changes the organization identity/profile.
	if err := s.Directory().Initialize(t.Context(), "organization-admin", ""); err != nil {
		t.Fatal(err)
	}
	current, err := s.Directory().Organization(t.Context(), "alice")
	if err != nil || current.ID != org.ID || current.DisplayName != "Acme" {
		t.Fatal(current, err)
	}
}
