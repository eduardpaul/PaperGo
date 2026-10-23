package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dal/resources"
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
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Versioning: historyMode("major"), Kind: "list", Name: "Records"})
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
	var item resources.Resource
	if err = json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		list   bool
		fields []string
	}{
		{"/v1/resources/" + item.ID, false, []string{"values"}},
		{"/v1/resources/" + l.ID + "/children?filter_field=serial&filter_op=eq&filter_value=9007199254740993", true, []string{"values"}},
		{"/v1/items/" + item.ID + "/revisions", true, []string{"payload"}},
		{"/v1/items/" + item.ID + "/publications", true, []string{"snapshot", "values"}},
	} {
		response = request(h, "GET", tc.path, "", testToken, "", "")
		if response.Code != 200 {
			t.Fatal(tc.path, response.Code, response.Body.String())
		}
		raw := response.Body.Bytes()
		if tc.list {
			var page struct {
				Value []json.RawMessage `json:"value"`
			}
			if err = json.Unmarshal(raw, &page); err != nil || len(page.Value) != 1 {
				t.Fatal(tc.path, response.Body.String(), err)
			}
			raw = page.Value[0]
		}
		var values map[string]json.RawMessage
		for _, field := range tc.fields {
			if err = json.Unmarshal(raw, &values); err != nil {
				t.Fatal(tc.path, err)
			}
			raw = values[field]
			values = nil
		}
		if err = json.Unmarshal(raw, &values); err != nil || string(values["serial"]) != "9007199254740993" || string(values["amount"]) != `"123.40"` {
			t.Fatal("incorrect JSON types or precision", tc.path, string(raw), err)
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
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Versioning: historyMode("major"), Kind: "list", Name: "Pages", PublishingEnabled: true})
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
