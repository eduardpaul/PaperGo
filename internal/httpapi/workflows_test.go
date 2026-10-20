package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/runner"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

func setupWorkflows(t *testing.T) (http.Handler, *dms.Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db := daltest.DatabaseAt(t, path)
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	wf := &workflow.Service{DMS: s}
	run, err := runner.New(runner.Config{DatabasePath: path, PollInterval: 20 * time.Millisecond}, db.SQL, s, wf)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.Launch(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Shutdown(10 * time.Second) })
	a := &API{DMS: s, Workflows: wf, Runner: run, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), Ready: db.SQL.PingContext, MaxUpload: 32}
	return a.Handler(), s
}

func TestWorkflowRoutes(t *testing.T) {
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

	w := request(h, "GET", "/v1/workflow-catalog", "", testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"item.update"`) || !strings.Contains(w.Body.String(), `"items.expire"`) || !strings.Contains(w.Body.String(), `"type":"task.completed"`) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
	for _, typ := range []string{"permissions.changed", "term.changed"} {
		if !strings.Contains(w.Body.String(), `"type":"`+typ+`"`) {
			t.Fatalf("catalog missing %s: %s", typ, w.Body)
		}
	}
	body := `{"name": "Keyword on demand", "definition": {
		"triggers": [{"type": "manual", "collection_id": "` + list.ID + `"}],
		"input_schema": {"type": "object", "required": ["keyword"], "properties": {"keyword": {"type": "string", "minLength": 1}}},
		"flow": {"start": "keyword", "nodes": {"keyword": {"activity": "item.update", "inputs": {"keywords": ["{input:keyword}"]}}}}}}`
	w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", body, testToken, "", json_)
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created workflow.Workflow
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Key != "keyword-on-demand" {
		t.Fatalf("created: %+v %v", created, err)
	}
	bad := strings.Replace(body, `"item.update"`, `"item.teleport"`, 1)
	if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", bad, testToken, "", json_); w.Code != 422 {
		t.Fatalf("unknown activity: %d %s", w.Code, w.Body)
	}
	if w = request(h, "PUT", "/v1/workflows/"+created.ID, body, testToken, "", json_); w.Code != 428 {
		t.Fatalf("update without If-Match: %d", w.Code)
	}
	renamed := strings.Replace(body, "Keyword on demand", "Keyword items", 1)
	if w = request(h, "PUT", "/v1/workflows/"+created.ID, renamed, testToken, `"1"`, json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"current_version":1`) {
		t.Fatalf("rename keeps the version: %d %s", w.Code, w.Body)
	}

	w = request(h, "POST", "/v1/workflows/"+created.ID+"/runs", `{"item_ids": ["`+item.ID+`"], "inputs": {"keyword": "approved"}}`, testToken, "", json_)
	if w.Code != 202 {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var started struct {
		RunIDs []string `json:"run_ids"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &started); err != nil || len(started.RunIDs) != 1 {
		t.Fatalf("started: %s %v", w.Body, err)
	}
	var run runner.Run
	for deadline := time.Now().Add(20 * time.Second); run.Status != "completed"; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("run did not complete: %+v", run)
		}
		w = request(h, "GET", "/v1/workflow-runs/"+started.RunIDs[0], "", testToken, "", "")
		if w.Code != 200 {
			t.Fatalf("run: %d %s", w.Code, w.Body)
		}
		if err = json.Unmarshal(w.Body.Bytes(), &run); err != nil {
			t.Fatal(err)
		}
	}
	if len(run.Steps) != 3 {
		t.Fatalf("steps: %+v", run.Steps)
	}
	got, err := s.Get(ctx, "alice", item.ID)
	if err != nil || keywordLabels(t, s, got.Keywords) != "approved" {
		t.Fatalf("item keywords: %v %v", got.Keywords, err)
	}
	w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/workflow-runs?workflow_id="+created.ID, "", testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), started.RunIDs[0]) {
		t.Fatalf("runs: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/workflow-runs/"+started.RunIDs[0]+"/retry", "", testToken, "", ""); w.Code != 422 {
		t.Fatalf("retry of a completed run: %d %s", w.Code, w.Body)
	}

	w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/workflow-builtins", "", testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items.expire"`) {
		t.Fatalf("built-ins: %d %s", w.Code, w.Body)
	}
	if w = request(h, "DELETE", "/v1/workflows/"+created.ID, "", testToken, `"2"`, ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w = request(h, "GET", "/v1/workflows/"+created.ID, "", testToken, "", ""); w.Code != 404 {
		t.Fatalf("deleted workflow: %d", w.Code)
	}
}

func TestTagVocabularyRoute(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", ws.ID, dms.CreateResource{Kind: "list", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, keywords := range [][]string{{"finance", "legal"}, {"finance"}} {
		if _, err = s.Create(ctx, "alice", list.ID, dms.CreateResource{Kind: "item", Name: keywords[len(keywords)-1], Keywords: keywords}); err != nil {
			t.Fatal(err)
		}
	}
	w := request(h, "GET", "/v1/workspaces/"+ws.ID+"/keywords?prefix=fin&collection_id="+list.ID, "", testToken, "", "")
	var vocabulary struct {
		Value []dms.WorkspaceKeywordCount `json:"value"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &vocabulary); err != nil || w.Code != 200 || len(vocabulary.Value) != 1 || vocabulary.Value[0].Name != "finance" || vocabulary.Value[0].Count != 2 {
		t.Fatalf("keywords: %d %s", w.Code, w.Body)
	}
}

func TestDecisionCatalogAndStaticValidation(t *testing.T) {
	h, s := setupWorkflows(t)
	response := request(h, "GET", "/v1/workflow-catalog", "", testToken, "", "")
	var catalog struct {
		Activities []workflow.Activity `json:"activities"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	found := map[string]workflow.Activity{}
	for _, activity := range catalog.Activities {
		found[activity.Key] = activity
	}
	for _, key := range []string{"ai.decide", "taxonomy.decide", "keywords.apply"} {
		a, ok := found[key]
		if !ok || a.OutputSchema == nil {
			t.Fatal("missing decision catalog entry", key)
		}
	}
	if _, ok := found["keywords.suggest"]; ok {
		t.Fatal("replaced action in catalog")
	}
	data := found["ai.decide"].InputSchema["properties"].(map[string]any)["data"].(map[string]any)
	if data["additionalProperties"] != true {
		t.Fatal("JSON data must be open")
	}
	ws, err := s.Create(t.Context(), "alice", "", dms.CreateResource{Kind: "workspace", Name: "Decisions"})
	if err != nil {
		t.Fatal(err)
	}
	const definition = `{"triggers":[{"type":"manual"}],"flow":{"start":"decide","nodes":{"decide":{"activity":"ai.decide","inputs":{"data":{"zero":0,"enabled":false},"questions":[{"key":"route","instructions":"Which route?","options":[{"key":"a","text":"A"},{"key":"b","text":"B"}]}]}}}}}`
	body := `{"name":"Decision","definition":` + definition + `}`
	saved := request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", body, testToken, "", "application/json")
	if saved.Code != 201 {
		t.Fatal(saved.Code, saved.Body.String())
	}
	invalid := strings.Replace(body, `"key":"b"`, `"key":"a"`, 1)
	bad := request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", invalid, testToken, "", "application/json")
	if bad.Code != 422 {
		t.Fatal("duplicate option saved", bad.Code, bad.Body.String())
	}
	var workflowRow workflow.Workflow
	if err = json.Unmarshal(saved.Body.Bytes(), &workflowRow); err != nil {
		t.Fatal(err)
	}
	reopened := request(h, "GET", "/v1/workflows/"+workflowRow.ID, "", testToken, "", "")
	if reopened.Code != 200 || !strings.Contains(reopened.Body.String(), `"enabled":false`) {
		t.Fatal(reopened.Body.String())
	}
}
