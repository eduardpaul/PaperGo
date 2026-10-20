package httpapi

import "net/http"

func (a *API) registerStorage(api *router) {
	api.HandleFunc("GET /v1/storage/status", a.storageStatus)
}

func (a *API) storageStatus(w http.ResponseWriter, r *http.Request) {
	out, err := a.dms(r).StorageStatus(r.Context(), subject(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}
