package httpapi

import (
	"context"
	"encoding/json"
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
	// Both lists accept Item, each through a list type of its own.
	d, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "D"})
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
	if r.Code != 412 {
		t.Fatal(r.Code, r.Body.String())
	}
	folder, err := s.Create(ctx, "alice", d.ID, dms.CreateResource{Kind: "folder", Name: "Archive"})
	if err != nil {
		t.Fatal(err)
	}
	selection := `{"parent_id":"` + folder.ID + `","items":[{"id":"` + i.ID + `","version":2}]}`
	moved := request(h, "POST", "/v1/resources/"+d.ID+"/move-selection", selection, testToken, "", "application/json")
	var result []dms.MoveSelectionItem
	if moved.Code != 200 || json.Unmarshal(moved.Body.Bytes(), &result) != nil || len(result) != 1 || result[0].Version != 3 {
		t.Fatal(moved.Code, moved.Body.String())
	}
	stale := request(h, "POST", "/v1/resources/"+d.ID+"/move-selection", selection, testToken, "", "application/json")
	if stale.Code != 412 {
		t.Fatal(stale.Code, stale.Body.String())
	}

}
