package httpapi

import (
	"context"
	"encoding/json"
	"papergo/internal/dms"
	"testing"
)

func TestMultiSortAndSelectAPI(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, e := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Query"})
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Rows"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateField(ctx, "alice", c.ID, dms.CreateField{Key: "n", Label: "N", Type: "integer", Indexed: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateField(ctx, "alice", c.ID, dms.CreateField{Key: "note", Label: "Note", Type: "note"}); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"B", "A"} {
		if _, e = s.Create(ctx, "alice", c.ID, dms.CreateResource{Kind: "item", Name: name, Values: map[string]any{"n": json.Number("9007199254740993"), "note": "detail"}}); e != nil {
			t.Fatal(e)
		}
	}
	path := "/v1/resources/" + c.ID + "/query"
	body := `{"query":{"sort":[{"field":"n","direction":"desc"},{"field":"$name","direction":"asc"}]},"select":["n"],"limit":1,"include_total":true}`
	res := request(h, "POST", path, body, testToken, "", "application/json")
	var result dms.QueryResult
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &result) != nil || len(result.Data) != 1 || result.Data[0].Name != "A" || len(result.Data[0].Values) != 1 || result.Total == nil || *result.Total != 2 || result.NextCursor == "" {
		t.Fatal(res.Code, res.Body.String())
	}
	requestBody := map[string]any{"query": map[string]any{"sort": []dms.SortSpec{{Field: "n", Direction: "desc"}, {Field: "$name", Direction: "asc"}}}, "select": []string{"n"}, "after": result.NextCursor, "limit": 1}
	raw, _ := json.Marshal(requestBody)
	res = request(h, "POST", path, string(raw), testToken, "", "application/json")
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &result) != nil || result.Data[0].Name != "B" {
		t.Fatal(res.Code, res.Body.String())
	}
	res = request(h, "POST", path, `{"query":{"sort":{"field":"n"}}}`, testToken, "", "application/json")
	if res.Code != 400 {
		t.Fatal("object sort accepted", res.Code, res.Body.String())
	}
	res = request(h, "POST", path, `{"query":{},"select":["unknown"]}`, testToken, "", "application/json")
	if res.Code != 422 {
		t.Fatal(res.Code, res.Body.String())
	}
}
