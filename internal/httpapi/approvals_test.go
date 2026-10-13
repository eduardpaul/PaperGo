package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"papergo/internal/dms"
	"papergo/internal/runner"
	"papergo/internal/workflow"
)

func TestApprovalRoutes(t *testing.T) {
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
			"ask": {"activity": "approval", "inputs": {"title": "Check {item:name}"}, "next": {"approved": "tag"}},
			"tag": {"activity": "item.update", "inputs": {"tags": ["{step:ask.comment}"]}}}}}}`
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
		for deadline := time.Now().Add(20 * time.Second); run.Status != want; time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("run is %+v, want %s", run, want)
			}
			w = request(h, "GET", "/v1/workflow-runs/"+started.RunIDs[0], "", testToken, "", "")
			_ = json.Unmarshal(w.Body.Bytes(), &run)
		}
	}
	runStatus("waiting")
	w = request(h, "GET", "/v1/me/approvals?status=open", "", testToken, "", "")
	var mine struct {
		Value []workflow.Approval `json:"value"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &mine); err != nil || w.Code != 200 || len(mine.Value) != 1 || mine.Value[0].Title != "Check Review" {
		t.Fatalf("my approvals: %d %s", w.Code, w.Body)
	}
	id := mine.Value[0].ID
	if w = request(h, "GET", "/v1/approvals/"+id, "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"assignees":["alice"]`) {
		t.Fatalf("approval: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/approvals/"+id+"/decision", `{"decision": "maybe"}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("bad decision: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/approvals/"+id+"/decision", `{"decision": "approve", "comment": "checked"}`, testToken, "", json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"approved"`) {
		t.Fatalf("decide: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/approvals/"+id+"/decision", `{"decision": "reject"}`, testToken, "", json_); w.Code != 409 {
		t.Fatalf("decide twice: %d %s", w.Code, w.Body)
	}
	runStatus("completed")
	if got, _ := s.Get(ctx, "alice", item.ID); strings.Join(got.Tags, ",") != "checked" {
		t.Fatalf("tags %v", got.Tags)
	}
	if w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/approvals", "", testToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatalf("workspace approvals: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/staged-files/missing", "", testToken, "", ""); w.Code != 404 {
		t.Fatalf("missing staged file: %d", w.Code)
	}
}
