package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/model"
	"strings"
	"testing"
)

func TestChoiceCardinalityIsImmutableAPI(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiple=%t", multiple), func(t *testing.T) {
			h, s, _ := setup(t)
			ctx := t.Context()
			workspace, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Choices"})
			if err != nil {
				t.Fatal(err)
			}
			list, err := s.Create(ctx, "alice", workspace.ID, dms.CreateResource{Kind: "list", Name: "Items"})
			if err != nil {
				t.Fatal(err)
			}
			field, err := s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"open"}, Options: model.FieldOptions{Multiple: multiple}})
			if err != nil {
				t.Fatal(err)
			}
			var value any = "open"
			if multiple {
				value = []any{"open"}
			}
			item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice", Values: map[string]any{"status": value}})
			if err != nil {
				t.Fatal(err)
			}
			list, err = s.Get(ctx, "alice", list.ID)
			if err != nil {
				t.Fatal(err)
			}
			response := request(h, "PATCH", "/v1/resources/"+list.ID+"/fields/"+field.ID, fmt.Sprintf(`{"options":{"multiple":%t}}`, !multiple), testToken, fmt.Sprintf(`"%d"`, list.Version), "application/json")
			if response.Code != 422 || !strings.Contains(response.Body.String(), "cardinality and reference scope are immutable") {
				t.Fatal(response.Code, response.Body.String())
			}
			if _, err = s.Client.FieldDefinition.UpdateOne(field).SetOptions(model.FieldOptions{Multiple: !multiple}).Save(ctx); err == nil {
				t.Fatal("database allowed a cardinality change")
			}
			current, err := s.Get(ctx, "alice", list.ID)
			if err != nil || current.Version != list.Version || *current.SchemaHeadID != *list.SchemaHeadID {
				t.Fatal("rejected edit changed collection schema", current, err)
			}
			current, err = s.Get(ctx, "alice", item.ID)
			if err != nil || current.Version != item.Version || *current.HeadRevisionID != *item.HeadRevisionID {
				t.Fatal("rejected edit changed item head", current, err)
			}
		})
	}
}

func TestMultipleFieldsAndPersonActivationAPI(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "People"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	fields := "/v1/resources/" + c.ID + "/fields"
	for _, typ := range []string{"text", "email", "url"} {
		body := fmt.Sprintf(`{"key":"%s","label":"Values","type":"%s","options":{"multiple":true}}`, typ, typ)
		res := request(h, "POST", fields, body, testToken, "", "application/json")
		if res.Code != 422 {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	res := request(h, "POST", fields, `{"key":"reviewers","label":"Reviewers","type":"person","indexed":true,"options":{"multiple":true}}`, testToken, "", "application/json")
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", "/v1/resources/"+c.ID+"/children", `{"kind":"item","name":"Review","values":{"reviewers":["bob","carol","bob"]}}`, testToken, "", "application/json")
	var item ent.Resource
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &item) != nil || len(item.Values["reviewers"].([]any)) != 2 {
		t.Fatal(res.Code, res.Body.String())
	}
	path := "/v1/identity/users/bob/status"
	res = request(h, "PUT", path, `{}`, testToken, "", "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PUT", path, `{"active":false}`, testToken, "", "application/json")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"active":false`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", path, "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"active":false`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/people", "", testToken, "", "")
	if res.Code != 200 || strings.Contains(res.Body.String(), `"bob"`) || !strings.Contains(res.Body.String(), `"carol"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PATCH", "/v1/resources/"+item.ID, `{"values":{"reviewers":["bob"]}}`, testToken, fmt.Sprintf(`"%d"`, item.Version), "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
	bob := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "bob"}, Logger: slog.Default()}).Handler()
	res = request(bob, "GET", "/v1/people", "", testToken, "", "")
	if res.Code != 403 {
		t.Fatal("inactive user retained module access", res.Code, res.Body.String())
	}
	res = request(h, "PUT", path, `{"active":true}`, testToken, "", "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(bob, "GET", "/v1/people", "", testToken, "", "")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/identity/users/missing/status", "", testToken, "", "")
	if res.Code != 404 {
		t.Fatal(res.Code, res.Body.String())
	}
}
