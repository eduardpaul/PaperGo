package httpapi

import (
	"encoding/json"
	"fmt"
	"testing"

	"papergo/ent"
	"papergo/internal/dms"
)

func TestChoiceWideningAPI(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Choices"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Items"})
	if err != nil {
		t.Fatal(err)
	}
	field, err := s.CreateField(ctx, "alice", list.ID, dms.CreateField{Key: "status", Label: "Status", Type: "choice", Choices: []string{"open", "closed"}, Indexed: true})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Invoice", Values: map[string]any{"status": "open"}})
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.Get(ctx, "alice", list.ID)
	if err != nil {
		t.Fatal(err)
	}
	response := request(h, "PATCH", "/v1/resources/"+list.ID+"/fields/"+field.ID, `{"options":{"multiple":true}}`, testToken, fmt.Sprintf(`"%d"`, list.Version), "application/json")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "GET", "/v1/resources/"+item.ID, "", testToken, "", "")
	var read ent.Resource
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &read) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	values, ok := read.Values["status"].([]any)
	if !ok || len(values) != 1 || values[0] != "open" {
		t.Fatal("choice was not widened", read.Values)
	}
}
