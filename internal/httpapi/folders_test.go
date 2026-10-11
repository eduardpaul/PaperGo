package httpapi

import (
	"context"
	"encoding/json"
	"papergo/ent"
	"papergo/internal/dms"
	"testing"
)

func TestFolderMetadataAPI(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "C", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateField(ctx, "alice", c.ID, dms.CreateField{Key: "notes", Label: "Notes", Type: "text", Indexed: true}); err != nil {
		t.Fatal(err)
	}
	r := request(h, "POST", "/v1/resources/"+c.ID+"/children", `{"kind":"folder","name":"Folder","values":{"notes":"archival"}}`, testToken, "", "application/json")
	var f ent.Resource
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &f) != nil || f.ContentTypeID == nil || f.Values["notes"] != "archival" {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "PATCH", "/v1/resources/"+f.ID, `{"values":{"notes":"current"}}`, testToken, `"1"`, "application/json")
	if r.Code != 200 || r.Header().Get("ETag") != `"2"` {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", "/v1/resources/"+c.ID+"/query", `{"query":{"filter":{"field":"notes","value":"current"}},"select":["notes"],"include_total":true}`, testToken, "", "application/json")
	var q dms.QueryResult
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &q) != nil || len(q.Data) != 1 || q.Data[0].ID != f.ID || q.Data[0].Values["notes"] != "current" {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "PATCH", "/v1/resources/"+f.ID, `{"values":{"notes":false}}`, testToken, `"2"`, "application/json")
	if r.Code != 422 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "PATCH", "/v1/resources/"+f.ID, `{"values":{"notes":null}}`, testToken, `"2"`, "application/json")
	f = ent.Resource{}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &f) != nil || len(f.Values) != 0 {
		t.Fatal(r.Code, r.Body.String())
	}
}
