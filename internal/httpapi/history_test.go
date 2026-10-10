package httpapi

import (
	"encoding/json"
	"testing"

	"papergo/ent"
	"papergo/internal/dms"
)

func TestNumberedRevisionReadAndRestoreAPI(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Team"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(t.Context(), "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(t.Context(), "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Original"})
	if err != nil {
		t.Fatal(err)
	}
	name := "Changed"
	item, err = s.Update(t.Context(), "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + item.ID + "/revisions"
	response := request(h, "GET", path+"/1", "", testToken, "", "")
	var revision dms.RevisionView
	if err = json.Unmarshal(response.Body.Bytes(), &revision); err != nil || response.Code != 200 || revision.Name != "Original" || revision.IsCurrent || len(revision.ChangedFields) == 0 {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if response = request(h, "POST", path+"/1/restore", "", testToken, "", ""); response.Code != 428 {
		t.Fatal(response.Code)
	}
	if response = request(h, "POST", path+"/1/restore", "", testToken, `"1"`, ""); response.Code != 409 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "POST", path+"/1/restore", "", testToken, `"2"`, "")
	var restored ent.Resource
	if err = json.Unmarshal(response.Body.Bytes(), &restored); err != nil || response.Code != 200 || restored.Name != "Original" || restored.Version != 3 || response.Header().Get("ETag") != `"3"` {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if response = request(h, "GET", path+"/4", "", testToken, "", ""); response.Code != 404 {
		t.Fatal(response.Code, response.Body.String())
	}
}
