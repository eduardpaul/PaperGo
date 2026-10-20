package httpapi

import (
	"encoding/json"
	"papergo/internal/dal/resources"
	"papergo/internal/dms"
	"strconv"
	"testing"
)

func TestCollectionHistorySettingsAPI(t *testing.T) {
	h, s, _ := setup(t)
	w, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "History"})
	if err != nil {
		t.Fatal(err)
	}
	res := request(h, "POST", "/v1/resources/"+w.ID+"/children", `{"kind":"list","name":"Records","versioning":"major","max_versions":1}`, testToken, "", "application/json")
	var c resources.Resource
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &c) != nil || c.MaxVersions != 1 || string(c.Versioning) != "major" {
		t.Fatal(res.Code, res.Body.String())
	}
	item, err := s.Create(t.Context(), "alice", c.ID, dms.CreateResource{Kind: "item", Name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	name := "Two"
	item, err = s.Update(t.Context(), "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + item.ID + "/revisions"
	res = request(h, "GET", path+"/1", "", testToken, "", "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", path+"/1/restore", "", testToken, strconv.Quote(strconv.Itoa(item.Version)), "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
	current, err := s.Get(t.Context(), "alice", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c = *current
	endpoint := "/v1/resources/" + c.ID
	keyword := strconv.Quote(strconv.Itoa(c.Version))
	for _, body := range []string{`{"max_versions":0}`, `{"max_versions":501}`, `{"versioning":"minor"}`} {
		res = request(h, "PATCH", endpoint, body, testToken, keyword, "application/json")
		if res.Code != 422 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	res = request(h, "PATCH", endpoint, `{"versioning":"off","max_versions":500}`, testToken, keyword, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", endpoint, `{"versioning":"major"}`, testToken, keyword, "application/json")
	if res.Code != 412 {
		t.Fatal(res.Code, res.Body.String())
	}
	name = "Three"
	_, err = s.Update(t.Context(), "alice", item.ID, item.Version, dms.UpdateResource{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	res = request(h, "GET", path+"/3", "", testToken, "", "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", path+"/2", "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
}

func historyMode(value string) *string { return &value }
