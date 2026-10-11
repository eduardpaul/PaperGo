package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"papergo/internal/dms"
	"testing"
)

func TestRESTRelationshipDirectAndWorkspaceQueries(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "W"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "L"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Link(ctx, "alice", a.ID, dms.CreateRelationship{Type: "related", TargetID: b.ID})
	if err != nil {
		t.Fatal(err)
	}
	r := request(h, "GET", "/v1/items/"+b.ID+"/relationships/"+e.ID, "", testToken, "", "")
	if r.Code != 200 || r.Header().Get("ETag") != `"1"` {
		t.Fatal(r.Code, r.Body.String(), r.Header())
	}
	var view dms.RelationshipView
	if err = json.Unmarshal(r.Body.Bytes(), &view); err != nil || view.RelatedItem.ID != a.ID || view.RelatedItem.CollectionID != l.ID {
		t.Fatal(view, err)
	}
	r = request(h, "GET", "/v1/workspaces/"+w.ID+"/relationships?$filter="+url.QueryEscape("id eq "+e.ID), "", testToken, "", "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var page dms.Page[dms.WorkspaceRelationshipView]
	if err = json.Unmarshal(r.Body.Bytes(), &page); err != nil || len(page.Data) != 1 || page.Data[0].SourceItem.ID == "" {
		t.Fatal(page, err)
	}
	for _, filter := range []string{"directed=maybe", "$filter=bad", "directed=true&directed=false"} {
		r = request(h, "GET", "/v1/workspaces/"+w.ID+"/relationships?"+filter, "", testToken, "", "")
		if r.Code != 400 {
			t.Fatal(filter, r.Code, r.Body.String())
		}
	}
}
