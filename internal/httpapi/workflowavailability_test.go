package httpapi

import (
	"encoding/json"
	eventstore "papergo/internal/dal/events"
	"papergo/internal/dms"
	"papergo/internal/workflow"
	"testing"
)

func TestWorkflowAvailabilityRoutesKeepManualAndAutomaticUseIndependent(t *testing.T) {
	h, s := setupWorkflows(t)
	ctx := t.Context()
	create := func(parent string, in dms.CreateResource) string {
		t.Helper()
		r, e := s.Create(ctx, "alice", parent, in)
		if e != nil {
			t.Fatal(e)
		}
		return r.ID
	}
	ws := create("", dms.CreateResource{Kind: "workspace", Name: "Workspace"})
	lib := create(ws, dms.CreateResource{Kind: "library", Name: "Photos"})
	item := create(lib, dms.CreateResource{Kind: "item", Name: "Photo"})
	definition := `{"triggers":[{"type":"manual","collection_id":"` + lib + `"},{"type":"item.updated","collection_id":"` + lib + `"}],"flow":{"start":"end","nodes":{"end":{"activity":"end"}}}}`
	response := request(h, "POST", "/v1/workspaces/"+ws+"/workflows", `{"name":"Reusable feature","definition":`+definition+`}`, testToken, "", "application/json")
	var w workflow.Workflow
	if response.Code != 201 || json.Unmarshal(response.Body.Bytes(), &w) != nil || w.AutomaticEnabled || !w.ManualEnabled {
		t.Fatal(response.Code, response.Body.String())
	}
	count := func() int {
		t.Helper()
		n, e := s.DAL.Events().Count(ctx, eventstore.Filter{Types: []string{"manual"}})
		if e != nil {
			t.Fatal(e)
		}
		return int(n)
	}
	before := count()
	body := `{"item_ids":["` + item + `"],"workflow_version":1}`
	response = request(h, "POST", "/v1/workflows/"+w.ID+"/check", body, testToken, "", "application/json")
	var availability workflow.Availability
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &availability) != nil || !availability.CanRun {
		t.Fatal(response.Code, response.Body.String())
	}
	response = request(h, "POST", "/v1/workspaces/"+ws+"/workflow-actions", body, testToken, "", "application/json")
	var page struct {
		Value []workflow.Action `json:"value"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	found := false
	for _, a := range page.Value {
		if a.Workflow != nil && a.Workflow.ID == w.ID {
			found = a.Availability.CanRun
		}
	}
	if !found {
		t.Fatal("manual-only workflow missing from actions", response.Body.String())
	}
	response = request(h, "POST", "/v1/workflows/"+w.ID+"/explain", `{"item_id":"`+item+`","event_type":"item.updated"}`, testToken, "", "application/json")
	var explanation workflow.Explanation
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &explanation) != nil || explanation.Eligible || explanation.Code != "automatic_off" {
		t.Fatal(response.Code, response.Body.String())
	}
	if count() != before {
		t.Fatal("read-only checks queued manual events")
	}
	response = request(h, "POST", "/v1/workflows/"+w.ID+"/runs", body, testToken, "", "application/json")
	if response.Code != 202 {
		t.Fatal(response.Code, response.Body.String())
	}
	if count() != before+1 {
		t.Fatal("explicit manual start must queue one event")
	}
}
