package httpapi

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	runstore "papergo/internal/dal/runs"
	"papergo/internal/dms"
	"papergo/internal/tasks"
	"papergo/internal/workflow"
)

func (a *API) registerWorkflows(api *router) {
	api.HandleFunc("GET /v1/workflow-catalog", a.workflowCatalog)
	api.HandleFunc("GET /v1/workspaces/{id}/workflows", a.workflows)
	api.HandleFunc("POST /v1/workspaces/{id}/workflows", a.createWorkflow)
	api.HandleFunc("GET /v1/workflows/{id}", a.getWorkflow)
	api.HandleFunc("PUT /v1/workflows/{id}", a.updateWorkflow)
	api.HandleFunc("DELETE /v1/workflows/{id}", a.deleteWorkflow)
	api.HandleFunc("GET /v1/workflows/{id}/versions", a.workflowVersions)
	api.HandleFunc("POST /v1/workflows/{id}/runs", a.idempotent(a.startWorkflow))
	api.HandleFunc("POST /v1/workspaces/{id}/workflow-calls", a.callWorkflow)
	api.HandleFunc("POST /v1/workflows/{id}/test-runs", a.testWorkflow)
	api.HandleFunc("POST /v1/workflows/{id}/check", a.checkWorkflow)
	api.HandleFunc("POST /v1/workflows/{id}/explain", a.explainWorkflow)
	api.HandleFunc("POST /v1/workspaces/{id}/workflow-actions", a.workflowActions)
	api.HandleFunc("GET /v1/workspaces/{id}/workflow-builtins", a.workflowBuiltIns)
	api.HandleFunc("PUT /v1/workspaces/{id}/workflow-builtins/{key}", a.setWorkflowBuiltIn)
	api.HandleFunc("POST /v1/workspaces/{id}/workflow-builtins/{key}/copy", a.copyWorkflowBuiltIn)
	api.HandleFunc("GET /v1/resources/{id}/workflow-roles", a.collectionWorkflowRoles)
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
	{"type": dms.EventFolderCreated, "description": "A folder was created; data has revision_id, revision_number and content_type_id, and the run has the folder as its item."},
	{"type": dms.EventFolderUpdated, "description": "A folder was renamed, retagged or moved; data has revision_id, revision_number and content_type_id for content changes, or from_parent_id and parent_id for a move. The run has the folder as its item."},
	{"type": dms.EventFolderDeleted, "description": "A folder was deleted, alone or with a parent folder; the run has no item."},
	{"type": dms.EventItemArchived, "description": "An item was archived."},
	{"type": dms.EventItemUnarchived, "description": "An item was unarchived."},
	{"type": dms.EventFolderRestored, "description": "A folder was restored from the recycle bin; the run has the folder as its item."},
	{"type": dms.EventFolderPurged, "description": "A deleted folder was permanently purged; the run has no item."},
	{"type": dms.EventFileAdded, "description": "An item received its first file attachment; data has source (upload, webdav, restore or workflow), file_version, blob_id, content_type, size, revision_id and content_type_id. sources limits the sources; the default is all but workflow."},
	{"type": dms.EventFileUpdated, "description": "An existing file was replaced, restored or reindexed; data has previous_blob_id, filename and the file.added payload. sources and media_types filter both file event types."},
	{"type": dms.EventPermissionsChanged, "description": "An item's or folder's effective permissions changed: its own or an ancestor's ACL was replaced (replace), it moved into another permission scope (move), or workspace membership changed (member.set, member.remove). Delivered once per affected item and folder, which is the run's item; data has resource_kind, action, root_id, root_kind, root_event_id, previous_scope_id and current_scope_id, and member_subject, previous_role and role for membership. actions limits the changes; collection_id, content_type_id and conditions apply as for item triggers."},
	{"type": workflow.TriggerSchedule, "description": "A cron occurrence (cron, time_zone). With collection_id, one run per item that meets the condition."},
	{"type": workflow.TriggerManual, "description": "Started through POST /v1/workflows/{id}/runs, with standalone (no items), per_item (exactly one), or selection (1–100 items) association; item modes may scope collection_id."},
	{"type": workflow.TriggerCall, "description": "Called by another run with ctx.call (POST /v1/workspaces/{id}/workflow-calls), on an item or none, with input checked against input_schema; collection_id requires an item of that collection. The caller owns the run and can wait for its output."},
	{"type": dms.EventTermMerged, "description": "A term was merged into another; data has source_term_id, target_term_id and term_set_id; the run has no item."},
	{"type": dms.EventTermMoved, "description": "A term moved under another parent or, promoted, into another set, which changes the parent terms its values apply; data has term_id, term_set_id, previous_term_set_id, parent_id and previous_parent_id (empty at the root); the run has no item."},
	{"type": dms.EventTermChanged, "description": "A term's name, localized labels or other editable properties changed; data has term_id, term_set_id, version, changed_fields, previous and current term properties. Delivered to every live workspace; the run has no item and the trigger takes no options. Subscriptions require taxonomy.read, rechecked for the workflow version author at dispatch."},
	{"type": tasks.EventCreated, "description": "A task was created; data has task_id, kind, key, assignees, source, item_id and run_id, and the run has the task's item. task_key limits it to tasks with that key."},
	{"type": tasks.EventCompleted, "description": "A task was completed; data as for task.created, with outcome and completed_by. task_key as for task.created."},
	{"type": tasks.EventCancelled, "description": "A task was cancelled; data as for task.created. task_key as for task.created."},
	{"type": "wf.{key}.{event}", "description": "Raised by runs of workflow key: completed, with the run's output as data.outputs, or failed, with data.error; carries the run's item."},
	{"type": "role.{role}.{event}", "description": "Raised by runs of the workflow that fills role, as for wf.{key}.{event}: completed or failed."},
}

func (a *API) workflowCatalog(w http.ResponseWriter, r *http.Request) {
	respond(w, r, 200, map[string]any{"triggers": triggerTypes, "builtins": workflow.BuiltIns(), "roles": workflow.Roles()})
}

// collectionWorkflowRoles shows which workflow fills each role for a list or
// library, with automatic availability independent of manual use.
func (a *API) collectionWorkflowRoles(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.CollectionRoles(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
}

func (a *API) workflows(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.List(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
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
	respond(w, r, 201, out)
}

func (a *API) getWorkflow(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.Get(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
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
	respond(w, r, 200, out)
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
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
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
	respond(w, r, 202, map[string]any{"run_ids": ids})
}

// callWorkflow starts runs of a called workflow for the workflow run that
// makes the request, in its transaction.
func (a *API) callWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.Calls
	if !a.decode(w, r, &in) {
		return
	}
	calls := &workflow.Service{DMS: a.dms(r), OCR: a.Workflows.OCR}
	ids, err := calls.StartCalls(r.Context(), subject(r), runOf(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 202, map[string]any{"run_ids": ids})
}

// testWorkflow starts a test run of a workflow's current version.
func (a *API) testWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.TestRun
	if !a.decode(w, r, &in) {
		return
	}
	id, err := a.Workflows.StartTest(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/v1/workflow-runs/"+id)
	respond(w, r, 202, map[string]any{"run_id": id})
}

func (a *API) workflowBuiltIns(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.ListBuiltIns(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out, "@odata.nextLink": nil})
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
	respond(w, r, 200, out)
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
	respond(w, r, 201, out)
}

func (a *API) workflowRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := queryInt(r, "$top")
	out, err := a.Workflows.Runs(r.Context(), subject(r), r.PathValue("id"), workflow.RunFilter{WorkflowID: q.Get("workflow_id"), ItemID: q.Get("item_id"), After: q.Get("$skiptoken"), Limit: limit, Origin: q.Get("origin"), Status: q.Get("status"), IncludeSystem: q.Get("include_system") == "true"})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

// maxRunWait bounds how long a request for a run waits for it to end,
// within the API's request timeout.
const maxRunWait = 25 * time.Second

// workflowRun returns a run. With wait (seconds), it answers once the run
// ended or the time passed, so a client that started a run can await its
// result without polling.
func (a *API) workflowRun(w http.ResponseWriter, r *http.Request) {
	var wait time.Duration
	if raw := r.URL.Query().Get("wait"); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds < 0 || time.Duration(seconds)*time.Second > maxRunWait {
			a.failure(w, r, dms.Invalid(fmt.Sprintf("wait is 0 to %d seconds", int(maxRunWait.Seconds()))))
			return
		}
		wait = time.Duration(seconds) * time.Second
	}
	deadline := time.Now().Add(wait)
	for poll := 100 * time.Millisecond; ; poll = min(2*poll, time.Second) {
		out, err := a.Workflows.Run(r.Context(), subject(r), r.PathValue("id"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		left := time.Until(deadline)
		if !slices.Contains(runstore.Active, out.Status) || left <= 0 {
			respond(w, r, 200, out)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(min(poll, left)):
		}
	}
}

func (a *API) cancelWorkflowRun(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.CancelRun(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) retryWorkflowRun(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.RetryRun(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 202, out)
}
