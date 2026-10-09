package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/ent"
	"papergo/internal/dms"
	"testing"
)

func TestSmartFolderRESTDefinitionsQueriesAndActions(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateField(ctx, "alice", l.ID, dms.CreateField{Key: "status", Label: "Status", Type: "text", Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"name":"Open","workspace_id":%q,"personal":false,"definition":{"filter":{"field":"status","value":"open"},"group_by":[{"field":"status"}]}}`, w.ID)
	response := request(h, "POST", "/v1/smart-folders", body, testToken, "", "application/json")
	if response.Code != 201 || response.Header().Get("ETag") != `"1"` {
		t.Fatal(response.Code, response.Body.String())
	}
	var f ent.SmartFolder
	if err = json.Unmarshal(response.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/smart-folders", "/v1/smart-folders/" + f.ID} {
		response = request(h, "GET", path, "", testToken, "", "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	response = request(h, "PUT", "/v1/smart-folders/"+f.ID, body, testToken, "", "application/json")
	if response.Code != 428 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "PUT", "/v1/smart-folders/"+f.ID, body, testToken, `"9"`, "application/json")
	if response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "POST", "/v1/smart-folders/"+f.ID+"/items", fmt.Sprintf(`{"folder_version":1,"collection_id":%q,"create":{"name":"Invoice"}}`, l.ID), testToken, "", "application/json")
	if response.Code != 201 || response.Header().Get("ETag") != `"1"` {
		t.Fatal(response.Code, response.Body.String())
	}
	var item ent.Resource
	if err = json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/smart-folders/" + f.ID + "/query", "/v1/smart-folders/" + f.ID + "/query/groups"} {
		response = request(h, "POST", path, `{}`, testToken, "", "application/json")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		response = request(h, "POST", path, `{"unexpected":true}`, testToken, "", "application/json")
		if response.Code != 422 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	path := "/v1/smart-folders/" + f.ID + "/items/" + item.ID
	response = request(h, "DELETE", path, `{"folder_version":1}`, testToken, "", "application/json")
	if response.Code != 428 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "DELETE", path, `{"folder_version":1}`, testToken, `"1"`, "application/json")
	if response.Code != 200 || response.Header().Get("ETag") != `"2"` {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	if response.Code != 200 {
		t.Fatal("unclassify deleted item", response.Code)
	}
	// Shared definitions are portable and import is protected by the workspace ETag.
	export := request(h, "GET", "/v1/workspaces/"+w.ID+"/smart-folders/export", "", testToken, "", "")
	if export.Code != 200 || export.Header().Get("ETag") == "" {
		t.Fatal(export.Code, export.Body.String())
	}
	importPath := "/v1/workspaces/" + w.ID + "/smart-folders/import"
	imported := request(h, "POST", importPath, export.Body.String(), testToken, "", "application/json")
	if imported.Code != 428 {
		t.Fatal(imported.Code, imported.Body.String())
	}
	imported = request(h, "POST", importPath, export.Body.String(), testToken, export.Header().Get("ETag"), "application/json")
	if imported.Code != 200 || imported.Header().Get("ETag") != export.Header().Get("ETag") {
		t.Fatal(imported.Code, imported.Body.String())
	}
	response = request(h, "DELETE", "/v1/smart-folders/"+f.ID, "", testToken, `"1"`, "")
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
}
