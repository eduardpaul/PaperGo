package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dms"
	"testing"
)

func TestDeleteMoveAndWebDAVSettings(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	response := request(h, "POST", "/v1/resources/"+w.ID+"/children", `{"kind":"library","name":"Files","webdav_enabled":true}`, testToken, "", "application/json")
	var lib struct {
		ID            string `json:"id"`
		WebDAVEnabled bool   `json:"webdav_enabled"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &lib); response.Code != 201 || err != nil || !lib.WebDAVEnabled {
		t.Fatal(response.Code, response.Body.String())
	}
	folder, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "folder", Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", lib.ID, dms.CreateResource{Kind: "item", Name: "Report.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	response = request(h, "PATCH", "/v1/resources/"+item.ID, `{"parent_id":"`+folder.ID+`"}`, testToken, `"1"`, "application/json")
	if response.Code != 200 || response.Header().Get("ETag") != `"2"` {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "POST", "/v1/resources/"+folder.ID+"/children", `{"kind":"item","name":"report.PDF"}`, testToken, "", "application/json")
	if response.Code != 409 {
		t.Fatal("duplicate library name accepted", response.Code)
	}
	response = request(h, "DELETE", "/v1/resources/"+folder.ID, "", testToken, "", "")
	if response.Code != 428 {
		t.Fatal(response.Code)
	}
	response = request(h, "DELETE", "/v1/resources/"+folder.ID, "", testToken, `"1"`, "")
	var conflict struct {
		Code string `json:"code"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &conflict); response.Code != 409 || err != nil || conflict.Code != "folder_not_empty" {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, id := range []string{folder.ID, item.ID} {
		if response = request(h, "GET", "/v1/resources/"+id, "", testToken, "", ""); response.Code != 200 {
			t.Fatal("rejected deletion changed resource", response.Code)
		}
	}
	response = request(h, "DELETE", "/v1/resources/"+item.ID, "", testToken, `"2"`, "")
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "DELETE", "/v1/resources/"+folder.ID, "", testToken, `"1"`, "")
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, id := range []string{folder.ID, item.ID} {
		if response = request(h, "GET", "/v1/resources/"+id, "", testToken, "", ""); response.Code != 404 {
			t.Fatal("deleted resource readable", response.Code)
		}
	}
	response = request(h, "DELETE", "/v1/resources/"+lib.ID, "", testToken, `"1"`, "")
	if response.Code != 204 {
		t.Fatal("library deletion failed", response.Code)
	}
}

func TestDeleteListFolderWithNestedFolder(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	folder, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "folder", Name: "Parent"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Create(ctx, "alice", folder.ID, dms.CreateResource{Kind: "folder", Name: "Child"})
	if err != nil {
		t.Fatal(err)
	}
	response := request(h, "DELETE", "/v1/resources/"+folder.ID, "", testToken, fmt.Sprintf(`"%d"`, folder.Version), "")
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response = request(h, "DELETE", "/v1/resources/"+child.ID, "", testToken, fmt.Sprintf(`"%d"`, child.Version), ""); response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	if response = request(h, "DELETE", "/v1/resources/"+folder.ID, "", testToken, fmt.Sprintf(`"%d"`, folder.Version), ""); response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
}
