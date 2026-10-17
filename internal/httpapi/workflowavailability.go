package httpapi

import (
	"net/http"
	"papergo/internal/workflow"
)

func (a *API) checkWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.Start
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.CheckStart(r.Context(), subject(r), r.PathValue("id"), in, true)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) workflowActions(w http.ResponseWriter, r *http.Request) {
	var in workflow.Start
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.Actions(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out})
}
func (a *API) explainWorkflow(w http.ResponseWriter, r *http.Request) {
	var in workflow.ExplainInput
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.Workflows.Explain(r.Context(), subject(r), r.PathValue("id"), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
