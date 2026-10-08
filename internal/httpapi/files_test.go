package httpapi

import (
	"context"
	"encoding/json"
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
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	for _, id := range []string{folder.ID, item.ID} {
		if response = request(h, "GET", "/v1/resources/"+id, "", testToken, "", ""); response.Code != 404 {
			t.Fatal("deleted resource readable", response.Code)
		}
	}
	response = request(h, "DELETE", "/v1/resources/"+lib.ID, "", testToken, `"1"`, "")
	if response.Code != 422 {
		t.Fatal("library deleted", response.Code)
	}
}

func TestWebDAVCredentialEndpoints(t *testing.T) {
	h, _, _ := setup(t)
	response := request(h, "POST", "/v1/webdav-credentials", `{"label":"Laptop"}`, testToken, "", "application/json")
	var created struct {
		ID         string `json:"id"`
		Password   string `json:"password"`
		SecretHash string `json:"secret_hash"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); response.Code != 201 || err != nil || created.Password == "" || created.SecretHash != "" {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "GET", "/v1/webdav-credentials", "", testToken, "", "")
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Data) != 1 || list.Data[0]["password"] != nil || list.Data[0]["label"] != "Laptop" {
		t.Fatal(response.Body.String())
	}
	if response = request(h, "POST", "/v1/webdav-credentials", `{"label":" "}`, testToken, "", "application/json"); response.Code != 422 {
		t.Fatal(response.Code)
	}
	if response = request(h, "DELETE", "/v1/webdav-credentials/"+created.ID, "", testToken, "", ""); response.Code != 204 {
		t.Fatal(response.Code)
	}
	if response = request(h, "DELETE", "/v1/webdav-credentials/"+created.ID, "", testToken, "", ""); response.Code != 404 {
		t.Fatal(response.Code)
	}
}
