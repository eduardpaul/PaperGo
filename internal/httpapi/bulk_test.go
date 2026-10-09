package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dms"
	"strings"
	"testing"
)

func TestRESTBulkAtomicPreconditionsAndStrictJSON(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Bulk"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records", PublishingEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/resources/" + l.ID + "/bulk"
	body := `{"operations":[{"action":"create","create":{"name":"A"}},{"action":"create","create":{"name":"B"}}]}`
	unauthorized := request(h, "POST", path, body, "", "", "application/json")
	if unauthorized.Code != 401 {
		t.Fatal("authentication", unauthorized.Code)
	}
	created := request(h, "POST", path, body, testToken, "", "application/json")
	var out dms.BulkResponse
	if created.Code != 200 || json.Unmarshal(created.Body.Bytes(), &out) != nil || len(out.Data) != 2 {
		t.Fatal("create", created.Code, created.Body.String())
	}
	if created.Header().Get("ETag") != "" {
		t.Fatal("bulk response must not have a collection ETag")
	}
	a, b := out.Data[0], out.Data[1]
	rollback := fmt.Sprintf(`{"operations":[{"action":"update","id":%q,"version":1,"update":{"name":"Changed"}},{"action":"delete","id":%q,"version":9}]}`, a.ID, b.ID)
	failed := request(h, "POST", path, rollback, testToken, "", "application/json")
	if failed.Code != 409 || !strings.Contains(failed.Body.String(), `"operation_index":1`) || strings.Contains(failed.Body.String(), `"data"`) {
		t.Fatal("indexed conflict", failed.Code, failed.Body.String())
	}
	got, err := s.Get(ctx, "alice", a.ID)
	if err != nil || got.Name != "A" || got.Version != 1 {
		t.Fatal("HTTP rollback", got, err)
	}
	// Unknown fields fail schema validation; anything but one JSON value is malformed.
	for body, status := range map[string]int{
		`{"operations":[{"action":"create","create":{"name":"C","kind":"folder"}}]}`:                                        422,
		fmt.Sprintf(`{"operations":[{"action":"update","id":%q,"version":1,"update":{"publishing_enabled":false}}]}`, a.ID): 422,
		`{"operations":[],"mode":"partial"}`: 422,
		`{"operations":[]} {}`:               400,
	} {
		res := request(h, "POST", path, body, testToken, "", "application/json")
		if res.Code != status {
			t.Fatal("strict decoding", res.Code, res.Body.String())
		}
	}
	for _, body := range []string{
		`{"operations":[]}`,
		fmt.Sprintf(`{"operations":[{"action":"delete","id":%q}]}`, a.ID),
		fmt.Sprintf(`{"operations":[{"action":"delete","id":%q,"version":0}]}`, a.ID),
		fmt.Sprintf(`{"operations":[{"action":"update","id":%q,"version":1,"update":{}}]}`, a.ID),
	} {
		res := request(h, "POST", path, body, testToken, "", "application/json")
		if res.Code != 422 {
			t.Fatal("invalid operation", res.Code, res.Body.String())
		}
	}
	publish := fmt.Sprintf(`{"operations":[{"action":"publish","id":%q,"version":1},{"action":"publish","id":%q,"version":1}]}`, a.ID, b.ID)
	res := request(h, "POST", path, publish, testToken, "", "application/json")
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil || out.Data[0].Version != 2 || out.Data[1].Version != 2 {
		t.Fatal("publish versions", res.Code, res.Body.String())
	}
	lifecycle := fmt.Sprintf(`{"operations":[{"action":"unpublish","id":%q,"version":2},{"action":"delete","id":%q,"version":2}]}`, a.ID, b.ID)
	res = request(h, "POST", path, lifecycle, testToken, "", "application/json")
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil || out.Data[0].Version != 3 || out.Data[1].Version != 3 {
		t.Fatal("lifecycle versions", res.Code, res.Body.String())
	}
}
