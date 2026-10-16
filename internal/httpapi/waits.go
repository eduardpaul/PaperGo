package httpapi

import (
	"encoding/json"
	"net/http"

	"papergo/internal/workflow"
)

func (a *API) listWaits(w http.ResponseWriter, r *http.Request) {
	out, err := a.Workflows.ListWaits(r.Context(), subject(r), workflow.WaitQuery{
		Kind:        r.URL.Query().Get("kind"),
		Queue:       r.URL.Query().Get("queue"),
		WorkspaceID: r.URL.Query().Get("workspace_id"),
		After:       r.URL.Query().Get("$skiptoken"),
		Limit:       queryInt(r, "$top"),
	})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, http.StatusOK, out)
}

func (a *API) claimWaits(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        string `json:"kind"`
		Key         string `json:"key"`
		WorkspaceID string `json:"workspace_id"`
		Holder      string `json:"holder"`
		Limit       int    `json:"limit"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	waits, token, err := a.Workflows.ClaimWaitsFor(r.Context(), subject(r), workflow.WaitClaim{
		Kind: in.Kind, Key: in.Key, WorkspaceID: in.WorkspaceID, Holder: in.Holder, Limit: in.Limit,
	})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	if waits == nil {
		waits = []workflow.Wait{}
	}
	respond(w, r, http.StatusOK, map[string]any{"value": waits, "claim_token": token})
}

func (a *API) updateWait(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        string          `json:"kind"`
		Key         string          `json:"key"`
		WorkspaceID string          `json:"workspace_id"`
		Data        json.RawMessage `json:"data"`
		Holder      string          `json:"holder"`
		ClaimToken  string          `json:"claim_token"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	err := a.Workflows.UpdateWait(r.Context(), subject(r), in.WorkspaceID, workflow.WaitUpdate{
		Kind: in.Kind, Key: in.Key, Data: in.Data, Holder: in.Holder, ClaimToken: in.ClaimToken,
	})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, http.StatusOK, map[string]any{"updated": true})
}

func (a *API) completeWait(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind        string              `json:"kind"`
		Key         string              `json:"key"`
		WorkspaceID string              `json:"workspace_id"`
		Completion  workflow.Completion `json:"completion"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.Workflows.FinishWait(r.Context(), subject(r), in.WorkspaceID, in.Kind, in.Key, in.Completion); err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, http.StatusOK, map[string]any{"completed": true})
}
