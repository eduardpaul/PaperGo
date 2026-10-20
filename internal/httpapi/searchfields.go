package httpapi

import (
	"net/http"
)

// registerSearchFields serves the fields and content types searches and
// smart folders filter on.
func (a *API) registerSearchFields(api *router) {
	api.HandleFunc("GET /v1/search/fields", a.searchFields)
	api.HandleFunc("GET /v1/search/content-types", a.searchContentTypes)
}

// searchFields lists the fields searches can filter on for the caller.
func (a *API) searchFields(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.dms(r).SearchFields(r.Context(), subject(r), q.Get("workspace_id"), q.Get("collection_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out})
}

func (a *API) searchContentTypes(w http.ResponseWriter, r *http.Request) {
	out, err := a.dms(r).SearchContentTypes(r.Context(), subject(r), r.URL.Query().Get("workspace_id"), r.URL.Query().Get("collection_id"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
