package httpapi

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestCollectionTemplateAPI(t *testing.T) {
	h, s, _ := setup(t)
	w, e := s.Create(context.Background(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Templates"})
	if e != nil {
		t.Fatal(e)
	}
	res := request(h, "GET", "/v1/collection-templates", "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"documents"`) || !strings.Contains(res.Body.String(), `"contacts"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", "/v1/resources/"+w.ID+"/children", `{"name":"Address book","template_key":"contacts"}`, testToken, "", "application/json")
	var c ent.Resource
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &c) != nil || c.Kind != "list" || c.SchemaHeadID == nil {
		t.Fatal(res.Code, res.Body.String())
	}
	for _, path := range []string{"/v1/resources/" + c.ID + "/content-types", "/v1/resources/" + c.ID + "/views"} {
		res = request(h, "GET", path, "", testToken, "", "")
		if res.Code != 200 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
}
