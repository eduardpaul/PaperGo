package httpapi

import (
	"net/http"

	"papergo/internal/dms"
)

func (a *API) recycleBin(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.dms(r).RecycleBin(r.Context(), subject(r), r.PathValue("id"), dms.RecycleBinQuery{After: q.Get("$skiptoken"), Limit: queryInt(r, "$top"), TopLevel: q.Get("top_level") == "true"})
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
func (a *API) restoreDeleted(w http.ResponseWriter, r *http.Request) {
	recursive, ok := a.recursive(w, r)
	if !ok {
		return
	}
	out, err := a.dms(r).RestoreDeleted(r.Context(), subject(r), r.PathValue("id"), r.PathValue("itemID"), recursive)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
}
func (a *API) purgeDeleted(w http.ResponseWriter, r *http.Request) {
	recursive, ok := a.recursive(w, r)
	if !ok {
		return
	}
	if err := a.dms(r).PurgeDeleted(r.Context(), subject(r), r.PathValue("id"), r.PathValue("itemID"), recursive); err != nil {
		a.failure(w, r, err)
		return
	}
	if a.Storage != nil {
		if err := a.dms(r).CleanupPurgedBlobs(r.Context(), a.Storage); err != nil {
			a.Logger.Error("purged blob cleanup", "error", err)
		}
	}
	w.WriteHeader(204)
}
func (a *API) recursive(w http.ResponseWriter, r *http.Request) (bool, bool) {
	switch r.URL.Query().Get("recursive") {
	case "", "false":
		return false, true
	case "true":
		return true, true
	default:
		a.problem(w, r, 400, "invalid_request", "recursive must be true or false")
		return false, false
	}
}
