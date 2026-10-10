package httpapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/ent"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"strings"
	"testing"
)

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
