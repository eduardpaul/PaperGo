package httpapi

import (
	"context"
	"encoding/json"
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
	w := request(h, "POST", "/v1/resources/"+list.ID+"/query/aggregates", `{"query":{"group_by":"taken"},"interval":"day","time_zone":"Europe/Madrid","aggregates":[{"op":"avg","field":"outdoor"},{"op":"min","field":"outdoor","as":"coldest"}]}`, testToken, "", "application/json")
	want := `{"value":[{"key":"2026-01-02","count":2,"values":{"avg_outdoor":"2.25","coldest":"-1.5"}},{"key":"2026-01-03","count":1,"values":{"avg_outdoor":"2.5","coldest":"2.5"}}]}`
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != want {
		t.Fatalf("by day: %d %s", w.Code, w.Body)
	}
	for body, code := range map[string]int{
		`{"aggregates":[]}`: 422,
		`{"aggregates":[{"op":"sum","field":"outdoor"}],"unknown":true}`: 400,
		`{"query":{"group_by":"outdoor"},"interval":"day","aggregates":[{"op":"sum","field":"outdoor"}]}`: 422,
	} {
		if w = request(h, "POST", "/v1/resources/"+list.ID+"/query/aggregates", body, testToken, "", "application/json"); w.Code != code {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
}
