package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"papergo/internal/dms"
	"papergo/internal/tasks"
)

func TestTaskRoutes(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Tasks"})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	const json_ = "application/json"
	// A standalone task with a form, answered and then visible in history.
	w := request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"item_id": "`+item.ID+`", "title": "Confirm the amount", "key": "amount_check",
		"input_schema": {"type": "object", "required": ["amount"], "properties": {"amount": {"type": "number"}}}, "values": {"amount": 12.5}, "due_at": "2030-01-01T00:00:00Z"}`, testToken, "", json_)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"source":"user"`) || w.Header().Get("Location") == "" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var standalone tasks.View
	_ = json.Unmarshal(w.Body.Bytes(), &standalone)
	if standalone.OperationID != nil {
		t.Fatalf("a standalone task has no operation: %s", w.Body)
	}
	if w = request(h, "GET", "/v1/me/tasks?kind=task", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), standalone.ID) {
		t.Fatalf("my tasks: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/tasks/"+standalone.ID, "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"can_respond":true`) {
		t.Fatalf("task: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/tasks", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), standalone.ID) {
		t.Fatalf("workspace tasks: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/items/"+item.ID+"/tasks", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), standalone.ID) {
		t.Fatalf("item tasks: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/responses", `{"outcome": "maybe"}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("bad outcome: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"title": "Bad", "assignees": ["nobody"]}`, testToken, "", json_); w.Code != 422 || !strings.Contains(w.Body.String(), "unknown recipients") {
		t.Fatalf("unknown assignee: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"title": "Bad", "values": {"amount": 1}}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("values without a form: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/responses", `{"outcome": "done", "input": {"amount": 13}}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"input":{"amount":13}`) {
		t.Fatalf("respond: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/responses", `{"outcome": "done", "input": {"amount": 14}}`, testToken, "", json_); w.Code != 409 {
		t.Fatalf("respond twice: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/cancel", "", testToken, "", json_); w.Code != 409 {
		t.Fatalf("cancel a completed task: %d %s", w.Code, w.Body)
	}
	w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"title": "Later"}`, testToken, "", json_)
	_ = json.Unmarshal(w.Body.Bytes(), &standalone)
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/reassign", `{"assignees": ["nobody"]}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("reassign to an unknown person: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/reassign", `{"assignees": ["role:owners"]}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"assignees":["alice"]`) {
		t.Fatalf("reassign: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/cancel", "", testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/staged-files/missing", "", testToken, "", ""); w.Code != 404 {
		t.Fatalf("missing staged file: %d", w.Code)
	}
}
