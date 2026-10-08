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

func TestRESTContentTypesBulkValidationAndRemoval(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Types"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/resources/" + l.ID + "/content-types"
	body := `{"key":"invoice","name":"Invoice","field_keys":[],"is_default":false}`
	for _, test := range []struct {
		etag string
		code int
	}{{"", 428}, {`"9"`, 409}} {
		res := request(h, "POST", path, body, testToken, test.etag, "application/json")
		if res.Code != test.code {
			t.Fatal(res.Code, res.Body.String())
		}
	}
	res := request(h, "POST", path, body, testToken, `"1"`, "application/json")
	var typ ent.ContentType
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &typ) != nil || res.Header().Get("ETag") != `"1"` {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", path, "", testToken, "", "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), typ.ID) || !strings.Contains(res.Body.String(), `"key":"item"`) {
		t.Fatal(res.Code, res.Body.String())
	}
	fieldBody := fmt.Sprintf(`{"content_type_id":%q,"key":"code","label":"Code","type":"text","indexed":true,"options":{"unique":true}}`, typ.ID)
	res = request(h, "POST", "/v1/resources/"+l.ID+"/fields", fieldBody, testToken, "", "application/json")
	var field ent.FieldDefinition
	if res.Code != 201 || json.Unmarshal(res.Body.Bytes(), &field) != nil {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "GET", "/v1/content-types/"+typ.ID, "", testToken, "", "")
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &typ) != nil || res.Header().Get("ETag") != `"2"` {
		t.Fatal(res.Code, res.Body.String())
	}
	update := `{"name":"Invoice","field_keys":["code"],"rules":[{"key":"code_required","field":"code","op":"required_if","when_field":"code","when_value":"A","message":"Code required"}]}`
	res = request(h, "PUT", "/v1/content-types/"+typ.ID, update, testToken, `"2"`, "application/json")
	if res.Code != 200 || res.Header().Get("ETag") != `"3"` {
		t.Fatal(res.Code, res.Body.String())
	}
	bulkPath := "/v1/resources/" + l.ID + "/bulk"
	bulk := fmt.Sprintf(`{"operations":[{"action":"create","create":{"name":"One","content_type_id":%q,"values":{"code":"A"}}},{"action":"create","create":{"name":"Two","content_type_id":%q,"values":{"code":"A"}}}]}`, typ.ID, typ.ID)
	res = request(h, "POST", bulkPath, bulk, testToken, "", "application/json")
	if res.Code != 409 || !strings.Contains(res.Body.String(), `"operation_index":1`) {
		t.Fatal(res.Code, res.Body.String())
	}
	got, err := s.Query(ctx, "alice", l.ID, dms.QueryRequest{})
	if err != nil || got.Total != 0 {
		t.Fatal(got, err)
	}
	// A rule dependency rejects deletion without changing the collection ETag.
	l, err = s.Get(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	deletePath := "/v1/resources/" + l.ID + "/fields/" + field.ID
	res = request(h, "DELETE", deletePath, "", testToken, fmt.Sprintf(`"%d"`, l.Version), "")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "PUT", "/v1/content-types/"+typ.ID, `{"name":"Invoice","field_keys":["code"]}`, testToken, `"3"`, "application/json")
	if res.Code != 200 {
		t.Fatal(res.Code, res.Body.String())
	}
	l, err = s.Get(ctx, "alice", l.ID)
	if err != nil {
		t.Fatal(err)
	}
	res = request(h, "DELETE", deletePath, "", testToken, fmt.Sprintf(`"%d"`, l.Version), "")
	if res.Code != 204 || res.Header().Get("ETag") != fmt.Sprintf(`"%d"`, l.Version+1) {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", bulkPath, fmt.Sprintf(`{"operations":[{"action":"create","create":{"name":"Removed","content_type_id":%q,"values":{"code":"A"}}}]}`, typ.ID), testToken, "", "application/json")
	if res.Code != 422 || !strings.Contains(res.Body.String(), `"operation_index":0`) {
		t.Fatal(res.Code, res.Body.String())
	}
}
