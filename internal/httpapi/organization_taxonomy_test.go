package httpapi

import (
	"encoding/json"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"testing"
)

func TestOrganizationTaxonomyHTTPScopesAndGroupRevocation(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	d := s.Directory()
	group, err := s.CreateTermGroup(ctx, "alice", dms.TermGroupInput{Name: "Corporate"})
	if err != nil {
		t.Fatal(err)
	}
	set, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "topics", Name: "Topics", IsOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.CreateTermSet(ctx, "alice", dms.TermSetInput{GroupID: group.ID, Key: "closed", Name: "Closed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", identity.MemberRole, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	readers, err := d.SaveGroup(ctx, "alice", "", 0, identity.GroupInput{Name: "Taxonomy contributors"})
	if err != nil {
		t.Fatal(err)
	}
	role, err := d.SaveRole(ctx, "alice", "", 0, identity.RoleInput{Name: "Taxonomy reader", Scopes: []string{"taxonomy.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetAssignment(ctx, "alice", role.ID, identity.Principal{Type: "group", ID: readers.ID}, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetMember(ctx, "alice", readers.ID, identity.Principal{Type: "user", ID: "bob"}, false); err != nil {
		t.Fatal(err)
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	for _, path := range []string{"/v1/term-groups", "/v1/term-sets", "/v1/taxonomy/export", "/v1/keywords", "/v1/term-sets/" + set.ID + "/terms"} {
		res := request(bob, "GET", path, "", testToken, "", "")
		if res.Code != 200 {
			t.Fatal(path, res.Code, res.Body.String())
		}
		if path == "/v1/term-sets" {
			var page map[string]any
			if err = json.Unmarshal(res.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			for _, row := range page["data"].([]any) {
				if _, ok := row.(map[string]any)["workspace_id"]; ok {
					t.Fatal("workspace ownership in taxonomy response")
				}
			}
		}
	}
	res := request(bob, "POST", "/v1/term-sets/"+set.ID+"/terms", `{"name":"Idea"}`, testToken, "", "application/json")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	var term ent.Term
	if err = json.Unmarshal(res.Body.Bytes(), &term); err != nil {
		t.Fatal(err)
	}
	res = request(bob, "POST", "/v1/keywords", `{"name":"Budget"}`, testToken, "", "application/json")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(bob, "POST", "/v1/term-sets/"+closed.ID+"/terms", `{"name":"Denied"}`, testToken, "", "application/json")
	if res.Code != 403 {
		t.Fatal("closed-set curation", res.Code, res.Body.String())
	}
	res = request(bob, "GET", "/v1/taxonomy/audit", "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal("reader audit access", res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/taxonomy/audit", "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	var audit struct {
		Data []ent.AuditEvent `json:"data"`
	}
	if err = json.Unmarshal(res.Body.Bytes(), &audit); err != nil || len(audit.Data) < 5 {
		t.Fatal(audit, err)
	}
	for _, e := range audit.Data {
		if e.WorkspaceID != "" || e.ResourceID != "" {
			t.Fatal(e)
		}
	}
	if err = d.SetMember(ctx, "alice", readers.ID, identity.Principal{Type: "user", ID: "bob"}, true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/term-groups", "/v1/term-sets", "/v1/terms?ids=" + term.ID, "/v1/keywords", "/v1/taxonomy/export"} {
		res = request(bob, "GET", path, "", testToken, "", "")
		if res.Code != 403 {
			t.Fatal("group revocation", path, res.Code, res.Body.String())
		}
	}
}
