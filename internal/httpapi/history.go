package httpapi

import (
	"net/http"
	"strconv"

	"papergo/internal/dms"
)

func (a *API) revision(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		a.failure(w, r, dms.ErrNotFound)
		return
	}
	out, err := a.dms(r).Revision(r.Context(), subject(r), r.PathValue("id"), number)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, out)
}

func (a *API) restoreRevision(w http.ResponseWriter, r *http.Request) {
	version, ok := a.version(w, r)
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("number"))
	if err != nil {
		a.failure(w, r, dms.ErrNotFound)
		return
	}
	out, err := a.dms(r).RestoreRevision(r.Context(), subject(r), r.PathValue("id"), number, version)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, out.Version)
	respond(w, r, 200, out)
}
