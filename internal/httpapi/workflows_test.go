package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/operations"
	"papergo/internal/dms"
	"papergo/internal/storage"
	"papergo/internal/workflow"
)

// workflowScript is a workflow whose export declares triggers, a JSON
// array, and which does nothing.
func workflowScript(triggers string) string {
	return `import { workflow } from "@papergo/workflow";
export default workflow({ triggers: ` + triggers + `, run: () => ({}) });
`
}

// workflowBody is a workflow's create or update body.
func workflowBody(t *testing.T, name, source string, extra map[string]any) string {
	t.Helper()
	body := map[string]any{"name": name, "source": source}
	for k, v := range extra {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// acceptRuns registers a run handler that never executes, so routes can
// start runs without the script runner.
func acceptRuns(t *testing.T, s *dms.Service) {
	t.Helper()
	if err := s.RegisterOperationHandler(dms.OperationKindRun, dms.OperationHandler{Scope: "workspace.read",
		Validate: func(context.Context, *dms.Service, string, string, json.RawMessage) error { return nil },
		Execute:  func(context.Context, *dms.Service, *operations.Operation) error { return nil }}); err != nil {
		t.Fatal(err)
	}
}

func setupWorkflows(t *testing.T) (http.Handler, *dms.Service) {
	t.Helper()
	db := daltest.DatabaseAt(t, filepath.Join(t.TempDir(), "test.db"))
	store, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := dms.NewService(db.SQL)
	acceptRuns(t, s)
	a := &API{DMS: s, Workflows: &workflow.Service{DMS: s}, Auth: auth.Development{Token: testToken, Subject: "alice"}, Storage: store, Logger: slog.New(slog.NewJSONHandler(testLog{t}, nil)), Ready: db.SQL.PingContext, MaxUpload: 32}
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
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items.expire"`) || !strings.Contains(w.Body.String(), `"type":"task.completed"`) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
	for _, typ := range []string{"permissions.changed", "term.changed"} {
		if !strings.Contains(w.Body.String(), `"type":"`+typ+`"`) {
			t.Fatalf("catalog missing %s: %s", typ, w.Body)
		}
	}
	source := `import { workflow } from "@papergo/workflow";

export default workflow<{ keyword: string }>({
  triggers: [{ type: "manual", collection_id: "` + list.ID + `" }],
  input_schema: { type: "object", required: ["keyword"], properties: { keyword: { type: "string", minLength: 1 } } },
  async run(ctx, input) {
    await ctx.api.v1.resources.byId(ctx.itemId!).patch({ keywords: [input.keyword] });
  },
});
`
	body := workflowBody(t, "Keyword on demand", source, nil)
	w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", body, testToken, "", json_)
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created workflow.Workflow
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Key != "keyword-on-demand" || created.Source != source || len(created.Definition.Triggers) != 1 || created.Definition.InputSchema == nil {
		t.Fatalf("created: %+v %v", created, err)
	}
	for name, broken := range map[string]string{
		"syntax":    strings.Replace(source, "async run(ctx, input) {", "async run(ctx, input) {{", 1),
		"no export": `const x = 1;`,
		"trigger":   strings.Replace(source, `type: "manual"`, `type: "item.teleported"`, 1),
	} {
		if w = request(h, "POST", "/v1/workspaces/"+ws.ID+"/workflows", workflowBody(t, "Broken", broken, nil), testToken, "", json_); w.Code != 422 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w = request(h, "PUT", "/v1/workflows/"+created.ID, body, testToken, "", json_); w.Code != 428 {
		t.Fatalf("update without If-Match: %d", w.Code)
	}
	renamed := workflowBody(t, "Keyword items", source, nil)
	if w = request(h, "PUT", "/v1/workflows/"+created.ID, renamed, testToken, `"1"`, json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"current_version":1`) {
		t.Fatalf("rename keeps the version: %d %s", w.Code, w.Body)
	}
	edited := workflowBody(t, "Keyword items", strings.Replace(source, "[input.keyword]", "[input.keyword, \"edited\"]", 1), nil)
	if w = request(h, "PUT", "/v1/workflows/"+created.ID, edited, testToken, `"2"`, json_); w.Code != 200 || !strings.Contains(w.Body.String(), `"current_version":2`) {
		t.Fatalf("a new script is a new version: %d %s", w.Code, w.Body)
	}
	w = request(h, "GET", "/v1/workflows/"+created.ID+"/versions", "", testToken, "", "")
	var versions struct {
		Value []workflow.Version `json:"value"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &versions); err != nil || len(versions.Value) != 2 || versions.Value[1].Source != source {
		t.Fatalf("versions: %d %s", w.Code, w.Body)
	}

	if w = request(h, "POST", "/v1/workflows/"+created.ID+"/runs", `{"item_ids": ["`+item.ID+`"]}`, testToken, "", json_); w.Code != 422 {
		t.Fatalf("start without the required input: %d %s", w.Code, w.Body)
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
	w = request(h, "GET", "/v1/workflow-runs/"+started.RunIDs[0], "", testToken, "", "")
	var run workflow.Run
	if err = json.Unmarshal(w.Body.Bytes(), &run); err != nil || w.Code != 200 || run.Status != "queued" {
		t.Fatalf("a started run waits for the dispatcher: %d %s", w.Code, w.Body)
	}
	if w = request(h, "POST", "/v1/workflow-runs/"+started.RunIDs[0]+"/retry", "", testToken, "", ""); w.Code != 404 && w.Code != 422 {
		t.Fatalf("retry of a run that has not started: %d %s", w.Code, w.Body)
	}

	w = request(h, "GET", "/v1/workspaces/"+ws.ID+"/workflow-builtins", "", testToken, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items.expire"`) {
		t.Fatalf("built-ins: %d %s", w.Code, w.Body)
	}
	if w = request(h, "DELETE", "/v1/workflows/"+created.ID, "", testToken, `"3"`, ""); w.Code != 204 {
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
