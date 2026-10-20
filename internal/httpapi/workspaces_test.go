package httpapi

import (
	"encoding/json"
	"papergo/internal/dal/resources"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func TestWorkspaceDeletionDisablesWorkflows(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	wf := &workflow.Service{DMS: s}
	created, err := wf.Create(ctx, "alice", w.ID, workflow.Save{AutomaticEnabled: new(true), Name: "Manual", Definition: json.RawMessage(`{"triggers":[{"type":"manual"}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	response := request(h, "DELETE", "/v1/resources/"+w.ID, "", testToken, `"1"`, "")
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	stored, err := s.DAL.Workflows().Find(ctx, created.ID)
	if err != nil || stored.AutomaticEnabled || stored.DeletedAt == nil || stored.Version != created.Version+1 {
		t.Fatal("workflow remained active", stored, err)
	}
	if _, err = wf.Get(ctx, "alice", created.ID); err == nil {
		t.Fatal("deleted workflow visible")
	}
	versions, err := s.DAL.Workflows().Versions(ctx, created.ID)
	if err != nil || len(versions) != 1 {
		t.Fatal("immutable workflow history lost", versions, err)
	}
}

func TestWorkspaceMemberAPIAndMetadataLifecycle(t *testing.T) {
	h, _, _ := setup(t)
	created := request(h, "POST", "/v1/workspaces", `{"name":" Team ","description":"Shared work"}`, testToken, "", "application/json")
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var w resources.Resource
	if err := json.Unmarshal(created.Body.Bytes(), &w); err != nil {
		t.Fatal(err)
	}
	if w.Name != "Team" || w.Description != "Shared work" {
		t.Fatal(w.Name, w.Description)
	}
	path := "/v1/workspaces/" + w.ID + "/members"
	for _, body := range []string{`{"subject":"bob"}`, `{"subject":"bob","role":"visitor"}`, `{"subject":"bob","role":"visitor"}`} {
		r := request(h, "POST", path, body, testToken, "", "application/json")
		if r.Code != 204 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	listed := request(h, "GET", path, "", testToken, "", "")
	var members struct {
		Data []dms.WorkspaceMember `json:"value"`
	}
	if listed.Code != 200 || json.Unmarshal(listed.Body.Bytes(), &members) != nil || len(members.Data) != 2 || members.Data[0] != (dms.WorkspaceMember{Subject: "alice", Role: "owner"}) || members.Data[1] != (dms.WorkspaceMember{Subject: "bob", Role: "visitor"}) {
		t.Fatal(listed.Code, listed.Body.String())
	}
	for _, body := range []string{`{"subject":"bob","role":"reader"}`, `{"subject":" "}`} {
		r := request(h, "POST", path, body, testToken, "", "application/json")
		if r.Code != 422 {
			t.Fatal("invalid member accepted", r.Code, r.Body.String())
		}
	}
	r := request(h, "DELETE", path+"/alice", "", testToken, "", "")
	if r.Code != 409 {
		t.Fatal("last owner removed", r.Code, r.Body.String())
	}
	r = request(h, "POST", path, `{"subject":"alice","role":"member"}`, testToken, "", "application/json")
	if r.Code != 409 {
		t.Fatal("last owner demoted", r.Code, r.Body.String())
	}
	r = request(h, "DELETE", path+"/bob", "", testToken, "", "")
	if r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "DELETE", path+"/bob", "", testToken, "", "")
	if r.Code != 404 {
		t.Fatal("missing member", r.Code)
	}
	resourcePath := "/v1/resources/" + w.ID
	r = request(h, "PATCH", resourcePath, `{"description":""}`, testToken, created.Header().Get("ETag"), "application/json")
	if r.Code != 200 {
		t.Fatal("membership invalidated metadata ETag", r.Code, r.Body.String())
	}
	r = request(h, "DELETE", resourcePath, "", testToken, created.Header().Get("ETag"), "")
	if r.Code != 412 {
		t.Fatal("stale workspace delete accepted", r.Code)
	}
	r = request(h, "DELETE", resourcePath, "", testToken, `"2"`, "")
	if r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, endpoint := range []string{resourcePath, path} {
		r = request(h, "GET", endpoint, "", testToken, "", "")
		if r.Code != 404 {
			t.Fatal("deleted workspace visible", r.Code, r.Body.String())
		}
	}
}
