package httpapi

import (
	"context"
	"fmt"
	"testing"

	"papergo/internal/dms"
)

func TestReclassifyAndDetachHTTP(t *testing.T) {
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
	oldType := *i.ContentTypeID
	typ, err := s.CreateContentType(ctx, "alice", dms.ContentTypeInput{Key: "new", Name: "New"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.AttachContentType(ctx, "alice", c.ID, c.Version, dms.AttachContentTypeInput{ContentTypeID: typ.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := request(h, "PATCH", "/v1/resources/"+i.ID, `{"content_type_id":"`+typ.ID+`"}`, testToken, `"1"`, "application/json")
	if r.Code != 200 || r.Header().Get("ETag") != `"2"` {
		t.Fatal(r.Code, r.Body.String())
	}
	path := "/v1/resources/" + c.ID + "/content-types/" + oldType
	r = request(h, "DELETE", path, "", testToken, "", "")
	if r.Code != 428 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "DELETE", path, "", testToken, fmt.Sprintf(`"%d"`, c.Version), "")
	if r.Code != 204 || r.Header().Get("ETag") == "" {
		t.Fatal(r.Code, r.Body.String())
	}
}
