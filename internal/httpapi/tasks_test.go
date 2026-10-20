package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/runner"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
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
	body := `{"name": "Ask", "definition": {"triggers": [{"type": "manual", "collection_id": "` + list.ID + `"}],
		"flow": {"start": "ask", "nodes": {
			"ask": {"activity": "approval", "inputs": {"title": "Check {item:name}"}, "next": {"approved": "keyword"}},
			"keyword": {"activity": "item.update", "inputs": {"keywords": ["{step:ask.comment}"]}}}}}}`
	w := request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", body, testToken, "", json_)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created workflow.Workflow
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	w = request(h, "POST", "/v1/workflows/"+created.ID+"/runs", `{"item_ids": ["`+item.ID+`"]}`, testToken, "", json_)
	var started struct {
		RunIDs []string `json:"run_ids"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &started); err != nil || len(started.RunIDs) != 1 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	runStatus := func(want string) {
		t.Helper()
		var run runner.Run
		for deadline := time.Now().Add(20 * time.Second); string(run.Status) != want; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("run is %+v, want %s", run, want)
			}
			w = request(h, "GET", "/v1/workflow-runs/"+started.RunIDs[0], "", testToken, "", "")
			_ = json.Unmarshal(w.Body.Bytes(), &run)
		}
	}
	runStatus("waiting")
	w = request(h, "GET", "/v1/me/tasks?waiting=true&kind=approval", "", testToken, "", "")
	var mine struct {
		Value []tasks.View `json:"value"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &mine); err != nil || w.Code != 200 || len(mine.Value) != 1 || mine.Value[0].Title != "Check Review" {
		t.Fatalf("my tasks: %d %s", w.Code, w.Body)
	}
	id := mine.Value[0].ID
	if w = request(h, "GET", "/v1/tasks/"+id, "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"assignees":["alice"]`) || !strings.Contains(w.Body.String(), `"can_respond":true`) {
		t.Fatalf("task: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+id+"/responses", `{"outcome": "maybe"}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("bad outcome: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+id+"/responses", `{"outcome": "rejected"}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("rejection without a comment: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+id+"/responses", `{"outcome": "approved", "comment": "checked"}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"outcome":"approved"`) {
		t.Fatalf("respond: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+id+"/responses", `{"outcome": "rejected", "comment": "no"}`, testToken, "", json_); w.Code != 409 {
		t.Fatalf("respond twice: %d %s", w.Code, w.Body)
	}
	runStatus("completed")
	if got, _ := s.Get(ctx, "alice", item.ID); keywordLabels(t, s, got.Keywords) != "checked" {
		t.Fatalf("keywords %v", got.Keywords)
	}
	if w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/tasks", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatalf("workspace tasks: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/items/"+item.ID+"/tasks", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatalf("item tasks: %d %s", w.Code, w.Body)
	}

	// A standalone task with a form, answered and then visible in history.
	w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"item_id": "`+item.ID+`", "title": "Confirm the amount", "key": "amount_check",
		"input_schema": {"type": "object", "required": ["amount"], "properties": {"amount": {"type": "number"}}}, "values": {"amount": 12.5}, "due_at": "2030-01-01T00:00:00Z"}`, testToken, "", json_)
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"source":"user"`) || w.Header().Get("Location") == "" {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var standalone tasks.View
	_ = json.Unmarshal(w.Body.Bytes(), &standalone)
	if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"title": "Bad", "assignees": ["nobody"]}`, testToken, "", json_); w.Code != 422 || !strings.Contains(w.Body.String(), "unknown recipients") {
		t.Fatalf("unknown assignee: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/tasks", `{"title": "Bad", "values": {"amount": 1}}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("values without a form: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/tasks/"+standalone.ID+"/responses", `{"outcome": "done", "input": {"amount": 13}}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"input":{"amount":13}`) {
		t.Fatalf("respond: %d %s", w.Code, w.Body)
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
