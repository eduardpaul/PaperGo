package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dms"
	"testing"
)

func TestArchiveHTTPPreconditionsAndFiltering(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Archive test"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "library", Name: "Documents"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", c.ID, dms.CreateResource{Kind: "item", Name: "Record.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/items/" + item.ID + "/archive"
	if r := request(h, "POST", path, "", testToken, "", ""); r.Code != 428 {
		t.Fatal(r.Code, r.Body.String())
	}
	r := request(h, "POST", path, "", testToken, fmt.Sprintf(`"%d"`, item.Version), "")
	if r.Code != 200 || r.Header().Get("ETag") != `"2"` {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request(h, "POST", path, "", testToken, `"1"`, ""); r.Code != 412 {
		t.Fatal(r.Code, r.Body.String())
	}
	for _, url := range []string{"/v1/resources/" + c.ID + "/children", "/v1/resources?workspace_id=" + w.ID} {
		r = request(h, "GET", url, "", testToken, "", "")
		var page struct {
			Value []struct {
				ID string `json:"id"`
			} `json:"value"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil {
			t.Fatal(r.Code, r.Body.String())
		}
		for _, v := range page.Value {
			if v.ID == item.ID {
				t.Fatal("archived item in default discovery")
			}
		}
	}
	r = request(h, "GET", "/v1/archive?q=record", "", testToken, "", "")
	var page struct {
		Value []struct {
			ID       string `json:"id"`
			Archived bool   `json:"archived"`
			CanWrite bool   `json:"can_write"`
		} `json:"value"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &page) != nil || len(page.Value) != 1 || !page.Value[0].Archived || !page.Value[0].CanWrite {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request(h, "GET", "/v1/archive?archived_from=invalid", "", testToken, "", ""); r.Code != 422 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request(h, "GET", "/v1/resources/"+c.ID+"/children?archive=bogus", "", testToken, "", ""); r.Code != 422 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request(h, "GET", "/v1/resources/"+item.ID, "", testToken, "", ""); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = request(h, "POST", "/v1/items/"+item.ID+"/unarchive", "", testToken, `"2"`, ""); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
}
