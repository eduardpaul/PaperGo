package httpapi

import "net/http"

func (a *API) recycleBin(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.RecycleBin(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("$skiptoken"), queryInt(r, "$top"))
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
	out, err := a.DMS.RestoreDeleted(r.Context(), subject(r), r.PathValue("id"), r.PathValue("itemID"), recursive)
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
	if err := a.DMS.PurgeDeleted(r.Context(), subject(r), r.PathValue("id"), r.PathValue("itemID"), recursive); err != nil {
		a.failure(w, r, err)
		return
	}
	if a.Storage != nil {
		if err := a.DMS.CleanupPurgedBlobs(r.Context(), a.Storage); err != nil {
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
