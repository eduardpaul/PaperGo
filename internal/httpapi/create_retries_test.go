package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"papergo/ent"
	"papergo/internal/dms"
)

func TestCreateRetryHTTPStatusesAndETags(t *testing.T) {
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
	id := uuid.NewString()
	path := "/v1/resources/" + c.ID + "/children"
	body := `{"id":"` + id + `","kind":"item","name":"Original"}`
	r := request(h, "POST", path, body, testToken, "", "application/json")
	if r.Code != 201 || r.Header().Get("Location") != "/v1/resources/"+id || r.Header().Get("ETag") != `"1"` {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", path, `{"id":"`+id+`","kind":"item","name":"Different"}`, testToken, "", "application/json")
	var out ent.Resource
	if r.Code != 200 || r.Header().Get("ETag") != `"1"` || json.Unmarshal(r.Body.Bytes(), &out) != nil || out.Name != "Original" {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = s.Delete(ctx, "alice", id, 1, dms.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	r = request(h, "POST", path, body, testToken, "", "application/json")
	if r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", path, `{"id":"00000000-0000-0000-0000-000000000000","kind":"item","name":"Invalid"}`, testToken, "", "application/json")
	if r.Code != 422 {
		t.Fatal(r.Code, r.Body.String())
	}
}
