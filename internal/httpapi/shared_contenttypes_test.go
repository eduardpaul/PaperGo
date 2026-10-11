package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
	"testing"
)

func TestOrganizationContentTypeAPISharingAndScopeRevocation(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	role, e := s.Directory().SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Catalog curator", Scopes: []string{"contentType.read", "contentType.manage"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Directory().SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "carol"}, true); e != nil {
		t.Fatal(e)
	}
	if e = s.Directory().SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "carol"}, false); e != nil {
		t.Fatal(e)
	}
	curator := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "carol"}, Logger: slog.Default()}).Handler()
	res := request(curator, "POST", "/v1/fields", `{"key":"code","label":"Code","type":"text"}`, testToken, "", "application/json")
	var field ent.FieldDefinition
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &field) != nil || res.Header().Get("ETag") != `"1"` {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(curator, "POST", "/v1/content-types", fmt.Sprintf(`{"key":"invoice","name":"Invoice","field_ids":[%q]}`, field.ID), testToken, "", "application/json")
	var typ ent.ContentType
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &typ) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "container_id") || strings.Contains(res.Body.String(), "is_default") {
		t.Fatal("catalog ownership leaked collection data", res.Body.String())
	}
	w, e := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "First"})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Invoices", ContentTypeIDs: []string{typ.ID}})
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.Create(ctx, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Second"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, "bob", w.ID, dms.CreateResource{Kind: "library", Name: "Shared", ContentTypeIDs: []string{typ.ID}})
	if e != nil {
		t.Fatal(e)
	}
	res = request(curator, "GET", "/v1/resources/"+a.ID, "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal("curator unexpectedly has workspace access", res.Code)
	}
	res = request(curator, "GET", "/v1/content-types/"+typ.ID, "", testToken, "", "")
	if res.Code != 200 || res.Header().Get("ETag") != `"1"` {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(curator, "PATCH", "/v1/fields/"+field.ID, `{"label":"Invoice code"}`, testToken, `"1"`, "application/json")
	if res.Code != 200 || res.Header().Get("ETag") != `"2"` {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(curator, "PATCH", "/v1/fields/"+field.ID, `{"label":"Stale"}`, testToken, `"1"`, "application/json")
	if res.Code != 412 {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, c := range []*ent.Resource{a, b} {
		live, e := s.Client.Resource.Get(ctx, c.ID)
		if e != nil || live.Version != c.Version+1 {
			t.Fatal(live, e)
		}
	}
	res = request(h, "GET", "/v1/resources/"+a.ID+"/content-types", "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"is_default":true`) || !strings.Contains(res.Body.String(), typ.ID) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(curator, "GET", "/v1/content-types/audit", "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"field.update"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/taxonomy/audit", "", testToken, "", "")
	if res.Code != 200 || strings.Contains(res.Body.String(), `"field.`) || strings.Contains(res.Body.String(), `"content_type.`) {
		t.Fatal("catalog events leaked into taxonomy feed", res.Code, res.Body.String())
	}
	// Current role assignments are checked on every request, including catalog reads.
	if e = s.Directory().SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "user", ID: "carol"}, true); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"/v1/content-types", "/v1/fields", "/v1/content-types/" + typ.ID, "/v1/content-types/audit"} {
		res = request(curator, "GET", path, "", testToken, "", "")
		if res.Code != 403 {
			t.Fatal(path, res.Code, res.Body.String())
		}
	}
	res = request(curator, "PUT", "/v1/content-types/"+typ.ID, fmt.Sprintf(`{"name":"Changed","field_ids":[%q]}`, field.ID), testToken, `"2"`, "application/json")
	if res.Code != 403 {
		t.Fatal(res.Code, res.Body.String())
	}
}
