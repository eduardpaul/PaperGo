package httpapi

import (
	"net/http"
	"strconv"

	"papergo/internal/dms"
	"papergo/internal/runner"
	"papergo/internal/workflow"
)

func (a *API) registerWorkflows(api *http.ServeMux) {
	api.HandleFunc("GET /v1/workflow-catalog", a.workflowCatalog)
	api.HandleFunc("GET /v1/workspaces/{id}/workflows", a.workflows)
	api.HandleFunc("POST /v1/workspaces/{id}/workflows", a.createWorkflow)
	api.HandleFunc("GET /v1/workflows/{id}", a.getWorkflow)
	api.HandleFunc("PUT /v1/workflows/{id}", a.updateWorkflow)
	api.HandleFunc("DELETE /v1/workflows/{id}", a.deleteWorkflow)
	api.HandleFunc("GET /v1/workflows/{id}/versions", a.workflowVersions)
	api.HandleFunc("POST /v1/workflows/{id}/runs", a.startWorkflow)
	api.HandleFunc("GET /v1/workspaces/{id}/workflow-builtins", a.workflowBuiltIns)
	api.HandleFunc("PUT /v1/workspaces/{id}/workflow-builtins/{key}", a.setWorkflowBuiltIn)
	api.HandleFunc("POST /v1/workspaces/{id}/workflow-builtins/{key}/copy", a.copyWorkflowBuiltIn)
	api.HandleFunc("GET /v1/workspaces/{id}/workflow-runs", a.workflowRuns)
	api.HandleFunc("GET /v1/workflow-runs/{id}", a.workflowRun)
	api.HandleFunc("POST /v1/workflow-runs/{id}/cancel", a.cancelWorkflowRun)
	api.HandleFunc("POST /v1/workflow-runs/{id}/retry", a.retryWorkflowRun)
}

// triggerTypes documents the trigger types for the catalog.
var triggerTypes = []map[string]string{
	{"type": dms.EventItemCreated, "description": "An item was created; data has revision_id, revision_number, content_type_id and blob_id. terms and term_change limit it to revisions that hold, gain or lose terms."},
	{"type": dms.EventItemUpdated, "description": "An item's content changed (a new head revision); data as for item.created. terms and term_change as for item.created."},
	{"type": dms.EventItemPublished, "description": "An item revision was published; data has revision_id, revision_number and publication_id. terms and term_change as for item.created."},
	{"type": dms.EventItemUnpublished, "description": "An item was unpublished; data has revision_id."},
	{"type": dms.EventItemDeleted, "description": "An item was deleted, alone or with its folder; the run has no item."},
	{"type": workflow.TriggerSchedule, "description": "A cron occurrence (cron, time_zone). With collection_id, one run per item that meets the condition."},
	{"type": workflow.TriggerManual, "description": "Started through POST /v1/workflows/{id}/runs, on items or (without collection_id) with no item."},
	{"type": dms.EventTermMerged, "description": "A term was merged into another; data has source_term_id, target_term_id and term_set_id; the run has no item."},
	{"type": "wf.{key}.{event}", "description": "Raised by runs of workflow key: completed, failed, or an event.raise node's event; carries the run's item."},
}

func (a *API) workflowCatalog(w http.ResponseWriter, r *http.Request) {
	respond(w, 200, map[string]any{"triggers": triggerTypes, "activities": workflow.Activities(), "builtins": workflow.BuiltIns()})
}

func (a *API) workflows(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.List(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}

func (a *API) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.Save
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.Create(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/workflows/"+out.ID)
	etag(w, out.Version)
	respond(w, 201, out)
}

func (a *API) getWorkflow(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.Get(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, 200, out)
}

func (a *API) updateWorkflow(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in workflow.Save
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.Update(r.Context(), subject(r), r.PathValue("id"), version, in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, 200, out)
}

func (a *API) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	if err := a.Workflows.Delete(r.Context(), subject(r), r.PathValue("id"), version); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) workflowVersions(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.Versions(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}

func (a *API) startWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.Start
	if !a.decode(w, r, &in) {
		return
	}
	ids, err := a.Workflows.StartRuns(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 202, map[string]any{"run_ids": ids})
}

func (a *API) workflowBuiltIns(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.ListBuiltIns(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}

// setWorkflowBuiltIn needs If-Match once the built-in's workflow exists.
func (a *API) setWorkflowBuiltIn(w http.ResponseWriter, r *http.Request) {
	var in workflow.SetBuiltIn
	if r.Header.Get("If-Match") != "" {
		version, ok := a.version(w, r)
		if !ok {
			return
		}
		in.Version = version
	}
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.UpdateBuiltIn(r.Context(), subject(r), r.PathValue("id"), r.PathValue("key"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, 200, out)
}

func (a *API) copyWorkflowBuiltIn(w http.ResponseWriter, r *http.Request) {
	var in workflow.CopyBuiltIn
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.CopyBuiltIn(r.Context(), subject(r), r.PathValue("id"), r.PathValue("key"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/workflows/"+out.ID)
	etag(w, out.Version)
	respond(w, 201, out)
}

func (a *API) workflowRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	out, err := a.Runner.Runs(r.Context(), subject(r), r.PathValue("id"), runner.RunFilter{WorkflowID: q.Get("workflow_id"), ItemID: q.Get("item_id"), After: q.Get("after"), Limit: limit})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (a *API) workflowRun(w http.ResponseWriter, r *http.Request) {
	out, err := a.Runner.Run(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (a *API) cancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	out, err := a.Runner.Cancel(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}

func (a *API) retryWorkflowRun(w http.ResponseWriter, r *http.Request) {
	out, err := a.Runner.Retry(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 202, out)
}
