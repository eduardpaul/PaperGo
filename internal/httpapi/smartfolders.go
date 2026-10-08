package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) registerSmartFolders(m *http.ServeMux) {
	m.HandleFunc("POST /v1/smart-folders/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.SmartFolderQueryRequest, v int) (any, error) {
		return a.DMS.QuerySmartFolder(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/smart-folders/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.SmartFolderQueryRequest, v int) (any, error) {
		return a.DMS.SmartFolderGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/smart-folders", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.SmartFolders(r.Context(), subject(r), r.URL.Query().Get("workspace_id"), r.URL.Query().Get("after"), queryInt(r, "limit"))
	}))
	m.HandleFunc("POST /v1/smart-folders", foundationMutation(a, 201, false, func(r *http.Request, in dms.SmartFolderInput, v int) (any, error) {
		return a.DMS.CreateSmartFolder(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/smart-folders/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.SmartFolder(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/smart-folders/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.SmartFolderInput, v int) (any, error) {
		return a.DMS.UpdateSmartFolder(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/smart-folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if err := a.DMS.DeleteSmartFolder(r.Context(), subject(r), r.PathValue("id"), v); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
}
