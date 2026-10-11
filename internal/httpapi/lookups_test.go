package httpapi

import (
	"context"
	"fmt"
	"papergo/internal/dms"
	"testing"
)

func TestRESTCrossWorkspaceLookupDoesNotGrantTargetAccess(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Private"})
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Create(ctx, "bob", w.ID, dms.CreateResource{Kind: "list", Name: "Contacts", PublishingEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	i, e := s.Create(ctx, "bob", l.ID, dms.CreateResource{Kind: "item", Name: "Draft"})
	if e != nil {
		t.Fatal(e)
	}
	w, e = s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Source"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if e != nil {
		t.Fatal(e)
	}
	res := request(h, "POST", "/v1/resources/"+c.ID+"/fields", fmt.Sprintf(`{"key":"contact","label":"Contact","type":"lookup","options":{"lookup_container_id":%q}}`, l.ID), testToken, "", "application/json")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", "/v1/resources/"+c.ID+"/children", fmt.Sprintf(`{"kind":"item","name":"Linked","values":{"contact":%q}}`, i.ID), testToken, "", "application/json")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/resources/"+i.ID, "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal("reference granted read access", res.Code, res.Body.String())
	}
}
