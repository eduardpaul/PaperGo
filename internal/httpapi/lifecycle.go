package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) unpublish(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	out, err := a.DMS.Unpublish(r.Context(), subject(r), r.PathValue("id"), version)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, 200, out)
}
func (a *API) revisions(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Revisions(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "after_revision"), queryInt(r, "limit"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}
func (a *API) schemas(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.Schemas(r.Context(), subject(r), r.PathValue("id"), queryInt(r, "after_revision"), queryInt(r, "limit"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}
func (a *API) itemSchema(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.ItemSchema(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("surface"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, out)
}
func (a *API) updateField(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	var in dms.UpdateField
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.UpdateField(r.Context(), subject(r), r.PathValue("id"), r.PathValue("fieldID"), version, in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, version+1)
	respond(w, 200, out)
}
