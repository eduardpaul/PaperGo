package httpapi

import (
	"encoding/json"
	"slices"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestResourceReadsReportCallerAccess(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Access"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	access := func(token, id string) []string {
		t.Helper()
		r := request(h, "GET", "/v1/resources/"+id, "", token, "", "")
		var out struct {
			ID     string   `json:"id"`
			Access []string `json:"access"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.ID != id || out.Access == nil {
			t.Fatal(r.Code, r.Body.String())
		}
		return out.Access
	}
	all := []string{"read", "read_draft", "write", "publish", "manage"}
	for _, id := range []string{w.ID, l.ID, i.ID} {
		if got := access(testToken, id); !slices.Equal(got, all) {
			t.Fatal("owner access", id, got)
		}
	}
	token := func(subject string, scopes ...string) string {
		t.Helper()
		created, err := s.Directory().CreateAccessToken(ctx, subject, identity.AccessTokenInput{Name: subject, Scopes: scopes})
		if err != nil {
			t.Fatal(err)
		}
		return created.Secret
	}
	for subject, role := range map[string]string{"member": "member", "visitor": "visitor"} {
		if err = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: subject, Role: role}); err != nil {
			t.Fatal(err)
		}
	}
	member := token("member", "workspace.read", "item.read", "item.write")
	if got := access(member, l.ID); !slices.Equal(got, []string{"read", "read_draft", "write"}) {
		t.Fatal("member access", got)
	}
	if got := access(token("visitor", "workspace.read", "item.read", "item.write"), i.ID); !slices.Equal(got, []string{"read"}) {
		t.Fatal("visitor access", got)
	}
	// A token without item.write cannot change content, whatever the ACL grants.
	if got := access(token("member", "workspace.read", "item.read"), l.ID); !slices.Equal(got, []string{"read", "read_draft"}) {
		t.Fatal("read-only token access", got)
	}
	r := request(h, "PATCH", "/v1/resources/"+i.ID, `{"name":"Renamed"}`, member, `"1"`, "application/json")
	var updated struct {
		Name   string   `json:"name"`
		Access []string `json:"access"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &updated) != nil || updated.Name != "Renamed" || !slices.Equal(updated.Access, []string{"read", "read_draft", "write"}) {
		t.Fatal(r.Code, r.Body.String())
	}
}
