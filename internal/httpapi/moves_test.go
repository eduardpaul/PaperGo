package httpapi

import (
	"context"
	"testing"

	"papergo/internal/dms"
)

func TestMoveItemHTTP(t *testing.T) {
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
	d, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "D", ContentTypeIDs: []string{*i.ContentTypeID}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + i.ID + "/move"
	body := `{"collection_id":"` + d.ID + `"}`
	r := request(h, "POST", path, body, testToken, "", "application/json")
	if r.Code != 428 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", path, body, testToken, `"1"`, "application/json")
	if r.Code != 200 || r.Header().Get("ETag") != `"2"` {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", path, body, testToken, `"1"`, "application/json")
	if r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
}
