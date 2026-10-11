package httpapi

import (
	"testing"

	"papergo/internal/dms"
)

func TestWorkspaceMembershipRejectsInactiveUsersAndPromotions(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Membership"})
	if err != nil {
		t.Fatal(err)
	}
	d := s.Directory()
	if _, err = d.SetUserActive(ctx, "organization-admin", "bob", false); err != nil {
		t.Fatal(err)
	}
	base := "/v1/workspaces/" + w.ID + "/members"
	for _, role := range []string{"visitor", "member", "owner"} {
		response := request(h, "POST", base, `{"subject":"bob","role":"`+role+`"}`, testToken, "", "application/json")
		if response.Code != 422 {
			t.Fatal("inactive member accepted", response.Code, response.Body.String())
		}
	}
	if _, err = d.SetUserActive(ctx, "organization-admin", "bob", true); err != nil {
		t.Fatal(err)
	}
	if err = s.SetWorkspaceMember(ctx, "alice", w.ID, dms.WorkspaceMember{Subject: "bob", Role: "visitor"}); err != nil {
		t.Fatal(err)
	}
	if _, err = d.SetUserActive(ctx, "organization-admin", "bob", false); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"member", "owner"} {
		response := request(h, "POST", base, `{"subject":"bob","role":"`+role+`"}`, testToken, "", "application/json")
		if response.Code != 422 {
			t.Fatal("inactive promotion accepted", response.Code, response.Body.String())
		}
	}
	members, err := s.WorkspaceMembers(ctx, "alice", w.ID)
	if err != nil || len(members) != 2 {
		t.Fatal(members, err)
	}
	for _, member := range members {
		if member.Subject == "bob" && member.Role != "visitor" {
			t.Fatal("rejected promotion changed membership", members)
		}
	}
	response := request(h, "DELETE", base+"/bob", "", testToken, "", "")
	if response.Code != 204 {
		t.Fatal("inactive membership could not be removed", response.Code, response.Body.String())
	}
}
