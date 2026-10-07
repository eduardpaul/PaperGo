package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/ent"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestRESTExactValuesAndSchemaRoutes(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []dms.CreateField{{Key: "serial", Label: "Serial", Type: "integer", Indexed: true}, {Key: "amount", Label: "Amount", Type: "decimal", Scale: 2, Indexed: true}} {
		if _, err = s.CreateField(ctx, "alice", l.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	response := request(h, "POST", "/v1/resources/"+l.ID+"/children", `{"kind":"item","name":"Exact","values":{"serial":9007199254740993,"amount":123.4}}`, testToken, "", "application/json")
	if response.Code != 201 || !strings.Contains(response.Body.String(), `"serial":9007199254740993`) || !strings.Contains(response.Body.String(), `"amount":"123.40"`) {
		t.Fatal("precision lost in REST create", response.Code, response.Body.String())
	}
	var item ent.Resource
	if err = json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/resources/" + item.ID, "/v1/resources/" + l.ID + "/children?filter_field=serial&filter_op=eq&filter_value=9007199254740993", "/v1/items/" + item.ID + "/revisions", "/v1/items/" + item.ID + "/publications"} {
		response = request(h, "GET", path, "", testToken, "", "")
		if response.Code != 200 || !strings.Contains(response.Body.String(), `9007199254740993`) {
			t.Fatal("precision lost on read", path, response.Code, response.Body.String())
		}
	}
	response = request(h, "GET", "/v1/resources/"+l.ID+"/schemas", "", testToken, "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"revision_number":3`) {
		t.Fatal("schema route", response.Body.String())
	}
	response = request(h, "GET", "/v1/items/"+item.ID+"/schema", "", testToken, "", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"amount"`) {
		t.Fatal("paired schema route", response.Body.String())
	}
	fields, err := s.Fields(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Get(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	response = request(h, "PATCH", "/v1/resources/"+l.ID+"/fields/"+fields[0].ID, `{"label":"New label"}`, testToken, fmt.Sprintf(`"%d"`, list.Version), "application/json")
	if response.Code != 200 || response.Header().Get("ETag") != fmt.Sprintf(`"%d"`, list.Version+1) {
		t.Fatal("field route precondition", response.Code, response.Body.String())
	}
}

func TestRESTLifecycleETags(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Pages", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Page"})
	if err != nil {
		t.Fatal(err)
	}
	for n, path := range []string{"publications", "unpublish", "publications"} {
		response := request(h, "POST", "/v1/items/"+i.ID+"/"+path, "", testToken, fmt.Sprintf(`"%d"`, n+1), "")
		expected := 201
		if path == "unpublish" {
			expected = 200
		}
		if response.Code != expected || response.Header().Get("ETag") != fmt.Sprintf(`"%d"`, n+2) {
			t.Fatal("lifecycle ETag", response.Code, response.Body.String())
		}
	}
}
