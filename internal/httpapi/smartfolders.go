package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) registerSmartFolders(m *router) {
	m.HandleFunc("POST /v1/smart-folders/preview", foundationMutation(a, 200, false, func(r *http.Request, in dms.SmartFolderPreview, _ int) (any, error) {
		return a.dms(r).PreviewSmartFolder(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/workspaces/{id}/smart-folders/export", func(w http.ResponseWriter, r *http.Request) {
		out, version, err := a.dms(r).ExportSmartFolders(r.Context(), subject(r), r.PathValue("id"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, version)
		respond(w, r, 200, out)
	})
	m.HandleFunc("POST /v1/workspaces/{id}/smart-folders/import", foundationMutation(a, 200, true, func(r *http.Request, in dms.SmartFolderPackage, v int) (any, error) {
		return a.dms(r).ImportSmartFolders(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("POST /v1/smart-folders/{id}/items", func(w http.ResponseWriter, r *http.Request) {
		var in dms.SmartFolderDrop
		if !a.decode(w, r, &in) {
			return
		}
		out, created, err := a.dms(r).ClassifySmartFolder(r.Context(), subject(r), r.PathValue("id"), in)
		if err != nil {
			a.failure(w, r, err)
			return
		}
		etag(w, out.Version)
		status := 200
		if created {
			status = 201
		}
		respond(w, r, status, out)
	})
	m.HandleFunc("DELETE /v1/smart-folders/{id}/items/{itemID}", foundationMutation(a, 200, true, func(r *http.Request, in dms.SmartFolderUnclassify, v int) (any, error) {
		return a.dms(r).UnclassifySmartFolder(r.Context(), subject(r), r.PathValue("id"), r.PathValue("itemID"), in.FolderVersion, v)
	}))
	m.HandleFunc("POST /v1/smart-folders/{id}/query", foundationMutation(a, 200, false, func(r *http.Request, in dms.SmartFolderQueryRequest, v int) (any, error) {
		return a.dms(r).QuerySmartFolder(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("POST /v1/smart-folders/{id}/query/groups", foundationMutation(a, 200, false, func(r *http.Request, in dms.SmartFolderQueryRequest, v int) (any, error) {
		return a.dms(r).SmartFolderGroups(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("GET /v1/smart-folders", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).SmartFolders(r.Context(), subject(r), r.URL.Query().Get("workspace_id"), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
	}))
	m.HandleFunc("POST /v1/smart-folders", foundationMutation(a, 201, false, func(r *http.Request, in dms.SmartFolderInput, v int) (any, error) {
		return a.dms(r).CreateSmartFolder(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/smart-folders/{id}", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).SmartFolder(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/smart-folders/{id}", foundationMutation(a, 200, true, func(r *http.Request, in dms.SmartFolderInput, v int) (any, error) {
		return a.dms(r).UpdateSmartFolder(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/smart-folders/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := a.version(w, r)
		if !ok {
			return
		}
		if err := a.dms(r).DeleteSmartFolder(r.Context(), subject(r), r.PathValue("id"), v); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
}
