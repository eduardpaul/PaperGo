package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"papergo/internal/dms"
)

func TestRecycleBinHTTP(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", c.ID, dms.CreateResource{Kind: "item", Name: "I"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, "alice", i.ID, i.Version, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	base := "/v1/resources/" + c.ID + "/recycle-bin"
	r := request(h, "GET", base, "", testToken, "", "")
	var page dms.Page[dms.DeletedResource]
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Data[0].DeletedBy != "alice" {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", base+"/"+i.ID+"/restore", "", testToken, "", "")
	if r.Code != 200 || r.Header().Get("ETag") != `"3"` {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = s.Delete(ctx, "alice", i.ID, 3, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	r = request(h, "DELETE", base+"/"+i.ID, "", testToken, "", "")
	if r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", base+"/"+i.ID+"/restore", "", testToken, "", "")
	if r.Code != 404 {
		t.Fatal(r.Code, r.Body.String())
	}
}
