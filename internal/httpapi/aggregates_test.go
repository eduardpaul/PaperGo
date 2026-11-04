package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"papergo/internal/dms"
)

// The aggregates route works out a collection's figures by day and refuses
// requests it cannot answer, as problems.
func TestQueryAggregatesRoute(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Farm"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Temperatures"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []dms.CreateField{{Key: "outdoor", Label: "Outdoor", Type: "decimal", Scale: 1, Indexed: true}, {Key: "taken", Label: "Taken", Type: "datetime", Indexed: true}} {
		if _, err = s.CreateField(ctx, "alice", list.ID, f); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range []struct{ at, outdoor string }{{"2026-01-02T08:00:00Z", "-1.5"}, {"2026-01-02T14:00:00Z", "6.0"}, {"2026-01-03T08:00:00Z", "2.5"}} {
		if _, err = s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Temperature", Values: map[string]any{"taken": v.at, "outdoor": json.Number(v.outdoor)}}); err != nil {
			t.Fatal(err)
		}
	}
	w := request(h, "POST", "/v1/resources/"+list.ID+"/query/aggregates", `{"groups":[{"field":"taken","interval":"day","time_zone":"Europe/Madrid"}],"aggregates":[{"op":"avg","field":"outdoor"},{"op":"min","field":"outdoor","as":"coldest"}]}`, testToken, "", "application/json")
	want := `{"@odata.nextLink":null,"total":2,"value":[{"keys":[{"value":"2026-01-02"}],"count":2,"values":{"avg_outdoor":"2.25","coldest":"-1.5"}},{"keys":[{"value":"2026-01-03"}],"count":1,"values":{"avg_outdoor":"2.5","coldest":"2.5"}}]}`
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("by day: %d %s", w.Code, w.Body)
	}
	// The coldest day first, a page at a time.
	byCold := `{"groups":[{"field":"taken","interval":"day"}],"sort":[{"field":"coldest","direction":"desc"}],"aggregates":[{"op":"min","field":"outdoor","as":"coldest"}]}`
	w = request(h, "POST", "/v1/resources/"+list.ID+"/query/aggregates?$top=1", byCold, testToken, "", "application/json")
	var page struct {
		Value []struct {
			Keys []struct {
				Value string `json:"value"`
			} `json:"keys"`
		} `json:"value"`
		Next string `json:"@odata.nextLink"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 200 || len(page.Value) != 1 || page.Value[0].Keys[0].Value != "2026-01-03" || !strings.Contains(page.Next, "%24skiptoken=a1.") {
		t.Fatalf("the first page: %d %s", w.Code, w.Body)
	}
	next, err := url.Parse(page.Next)
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", next.RequestURI(), byCold, testToken, "", "application/json")
	if page.Next = ""; json.Unmarshal(w.Body.Bytes(), &page) != nil || w.Code != 200 || len(page.Value) != 1 || page.Value[0].Keys[0].Value != "2026-01-02" || page.Next != "" {
		t.Fatalf("the last page: %d %s", w.Code, w.Body)
	}
	for body, code := range map[string]int{
		`{"aggregates":[]}`: 422,
		`{"aggregates":[{"op":"sum","field":"outdoor"}],"unknown":true}`:                                  400,
		`{"groups":[{"field":"outdoor","interval":"day"}],"aggregates":[{"op":"sum","field":"outdoor"}]}`: 422,
	} {
		if w = request(h, "POST", "/v1/resources/"+list.ID+"/query/aggregates", body, testToken, "", "application/json"); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}

	view, err := s.CreateView(ctx, "alice", list.ID, dms.ViewInput{Name: "Warm", Layout: "table", Columns: []string{"outdoor"}, Query: dms.QuerySpec{Filter: &dms.FilterExpr{Field: "outdoor", Op: "gt", Value: json.RawMessage(`"0"`)}}})
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", "/v1/views/"+view.ID+"/query/aggregates", `{"aggregates":[{"op":"count","field":"outdoor"}]}`, testToken, "", "application/json")
	if want := `{"@odata.nextLink":null,"total":1,"value":[{"keys":[],"count":2,"values":{"count_outdoor":2}}]}`; w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("a view's figures: %d %s", w.Code, w.Body)
	}
	folder, err := s.CreateSmartFolder(ctx, "alice", dms.SmartFolderInput{Name: "All", WorkspaceID: &ws.ID, Definition: dms.SmartFolderDefinition{Mode: "query", Collections: []string{"Temperatures"}}})
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, "POST", "/v1/smart-folders/"+folder.ID+"/query/aggregates", `{"aggregates":[{"op":"max","field":"outdoor"}]}`, testToken, "", "application/json")
	if want := `{"@odata.nextLink":null,"total":1,"value":[{"keys":[],"count":3,"values":{"max_outdoor":"6.0"}}]}`; w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("a smart folder's figures: %d %s", w.Code, w.Body)
	}
	w = request(h, "POST", "/v1/smart-folders/"+folder.ID+"/query/compact", `{}`, testToken, "", "application/json")
	if w.Code != 200 || strings.Contains(w.Body.String(), "head_revision_id") || !strings.Contains(w.Body.String(), `"collection_name":"Temperatures"`) {
		t.Fatalf("a smart folder's compact items: %d %s", w.Code, w.Body)
	}
}
