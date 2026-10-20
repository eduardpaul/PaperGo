package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) workspaceMembers(w http.ResponseWriter, r *http.Request) {
	members, err := a.dms(r).WorkspaceMembers(r.Context(), subject(r), r.PathValue("id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, http.StatusOK, map[string]any{"value": members})
}

func (a *API) setWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	var in dms.WorkspaceMember
	if !a.decode(w, r, &in) {
		return
	}
	if err := a.dms(r).SetWorkspaceMember(r.Context(), subject(r), r.PathValue("id"), in); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) removeWorkspaceMember(w http.ResponseWriter, r *http.Request) {
	if err := a.dms(r).RemoveWorkspaceMember(r.Context(), subject(r), r.PathValue("id"), r.PathValue("member")); err != nil {
		a.failure(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
