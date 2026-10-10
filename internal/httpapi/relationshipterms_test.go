package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestRelationshipTaxonomyAPI(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, "bob", "", dms.CreateResource{Kind: "workspace", Name: "Private graph"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Create(ctx, "bob", w.ID, dms.CreateResource{Kind: "list", Name: "Nodes"})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Create(ctx, "bob", c.ID, dms.CreateResource{Kind: "item", Name: "A"})
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Create(ctx, "bob", c.ID, dms.CreateResource{Kind: "item", Name: "B"})
	if e != nil {
		t.Fatal(e)
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	res := request(bob, "POST", "/v1/items/"+a.ID+"/relationships", fmt.Sprintf(`{"type":"Related","target_id":%q}`, b.ID), testToken, "", "application/json")
	var edge ent.Relationship
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &edge) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/relationship-types", "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), edge.TypeID) || !strings.Contains(res.Body.String(), `"term"`) || strings.Contains(res.Body.String(), `"workspace_id"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/terms/"+edge.TypeID, "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"name":"Related"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	// The workspace catalog route and type_id input have been replaced outright.
	res = request(bob, "GET", "/v1/workspaces/"+w.ID+"/relationship-types", "", testToken, "", "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(bob, "POST", "/v1/items/"+a.ID+"/relationships", fmt.Sprintf(`{"type_id":%q,"target_id":%q}`, edge.TypeID, b.ID), testToken, "", "application/json")
	if res.Code != 400 {
		t.Fatal(res.Code, res.Body.String())
	}
}
