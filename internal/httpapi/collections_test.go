package httpapi

import (
	"encoding/json"
	"papergo/internal/dal/resources"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func TestCollectionSettingsAndDeleteAPI(t *testing.T) {
	h, s, _ := setup(t)
	workspace, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	created := request(h, "POST", "/v1/resources/"+workspace.ID+"/children", `{"kind":"list","name":" Records ","description":"Shared records","allow_folders":false}`, testToken, "", "application/json")
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var collection resources.Resource
	if err = json.Unmarshal(created.Body.Bytes(), &collection); err != nil {
		t.Fatal(err)
	}
	if collection.Name != "Records" || collection.Description != "Shared records" || collection.AllowFolders {
		t.Fatal(collection)
	}
	path := "/v1/resources/" + collection.ID
	blocked := request(h, "POST", path+"/children", `{"kind":"folder","name":"Folder"}`, testToken, "", "application/json")
	if blocked.Code != 422 {
		t.Fatal(blocked.Code, blocked.Body.String())
	}
	changed := request(h, "PATCH", path, `{"allow_folders":true,"description":"Updated"}`, testToken, created.Header().Get("ETag"), "application/json")
	if changed.Code != 200 {
		t.Fatal(changed.Code, changed.Body.String())
	}
	if err = json.Unmarshal(changed.Body.Bytes(), &collection); err != nil || !collection.AllowFolders || collection.Description != "Updated" {
		t.Fatal(collection, err)
	}
	wf := &workflow.Service{DMS: s}
	bound, err := wf.Create(t.Context(), "alice", workspace.ID, workflow.Save{AutomaticEnabled: new(true), Name: "Manual collection workflow", Source: workflowScript(`[{"type":"manual","collection_id":"` + collection.ID + `"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	deleted := request(h, "DELETE", path, "", testToken, changed.Header().Get("ETag"), "")
	if deleted.Code != 204 {
		t.Fatal(deleted.Code, deleted.Body.String())
	}
	if stored, err := s.DAL.Workflows().Find(t.Context(), bound.ID); err != nil || stored.AutomaticEnabled || stored.DeletedAt == nil {
		t.Fatal("bound workflow survived", stored, err)
	}
	if gone := request(h, "GET", path, "", testToken, "", ""); gone.Code != 404 {
		t.Fatal(gone.Code, gone.Body.String())
	}
}
