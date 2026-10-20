package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"

	"papergo/internal/dal/daltest"
	"papergo/internal/dal/workflows"
	"papergo/internal/dms"
	"papergo/internal/workflow"
)

func TestWorkflowWaitRoutes(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	ws, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Docs"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Write(ctx, func(tx *dms.Service) error {
		wf, err := tx.DAL.Workflows().Create(ctx, workflows.CreateInput{WorkspaceID: ws.ID, Key: "wait-review", Name: "Wait review", CreatedBy: "alice"})
		if err != nil {
			return err
		}
		if err = daltest.WorkflowRun(ctx, tx.DAL, "run-1", wf.ID, ws.ID, "alice", 1); err != nil {
			return err
		}
		return workflow.PutWait(ctx, tx, workflow.Wait{
			Kind: "review", Key: "one", WorkspaceID: ws.ID, RunID: "run-1", Node: "park", ExecutionID: "exec-1",
			Data: json.RawMessage(`{"n":0}`),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/v1/workflow-waits?kind=review", "", testToken, "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("installation list %d %s", w.Code, w.Body.String())
	}
	w := request(h, "GET", "/v1/workflow-waits?kind=review&workspace_id="+ws.ID, "", testToken, "", "")
	if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
		t.Fatalf("list %d %s", w.Code, w.Body.String())
	}
	var listed struct {
		Value []workflow.Wait `json:"value"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Value) != 1 || listed.Value[0].Key != "one" {
		t.Fatalf("listed %+v %v", listed, err)
	}
	w = request(h, "POST", "/v1/workflow-waits/claim", `{"kind":"review","workspace_id":"`+ws.ID+`","holder":"holder-1","limit":10}`, testToken, "", "application/json")
	if w.Code != http.StatusOK {
		t.Fatalf("claim %d %s", w.Code, w.Body.String())
	}
	var claimed struct {
		Value      []workflow.Wait `json:"value"`
		ClaimToken string          `json:"claim_token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &claimed); err != nil || len(claimed.Value) != 1 || claimed.ClaimToken == "" {
		t.Fatalf("claimed %+v %v", claimed, err)
	}
	body := `{"kind":"review","key":"one","workspace_id":"` + ws.ID + `","holder":"holder-1","claim_token":"` + claimed.ClaimToken + `","data":{"n":1}}`
	if w = request(h, "PATCH", "/v1/workflow-waits", body, testToken, "", "application/json"); w.Code != http.StatusOK {
		t.Fatalf("update %d %s", w.Code, w.Body.String())
	}
	if w = request(h, "POST", "/v1/workflow-waits/complete", `{"kind":"review","key":"one","workspace_id":"`+ws.ID+`","completion":{"output":{"ok":true}}}`, testToken, "", "application/json"); w.Code != http.StatusOK {
		t.Fatalf("complete %d %s", w.Code, w.Body.String())
	}
	w = request(h, "GET", "/v1/workflow-waits?kind=review&workspace_id="+ws.ID, "", testToken, "", "")
	if err = json.Unmarshal(w.Body.Bytes(), &listed); err != nil || w.Code != http.StatusOK || len(listed.Value) != 0 {
		t.Fatalf("open after complete %d %+v %v", w.Code, listed, err)
	}
}
