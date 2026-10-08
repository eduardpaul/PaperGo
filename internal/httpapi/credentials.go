package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

// WebDAV credentials belong to the calling principal, not to a workspace, so
// issuing and revoking them is logged rather than written to a workspace audit.

func (a *API) webdavCredentials(w http.ResponseWriter, r *http.Request) {
	out, err := a.DMS.WebDAVCredentials(r.Context(), subject(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, 200, map[string]any{"data": out})
}
func (a *API) createWebDAVCredential(w http.ResponseWriter, r *http.Request) {
	var in dms.WebDAVCredentialInput
	if !a.decode(w, r, &in) {
		return
	}
	out, err := a.DMS.CreateWebDAVCredential(r.Context(), subject(r), in)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	a.Logger.InfoContext(r.Context(), "webdav credential created", "credential_id", out.ID, "subject", out.Subject, "request_id", r.Context().Value(requestKey{}))
	respond(w, 201, out)
}
func (a *API) revokeWebDAVCredential(w http.ResponseWriter, r *http.Request) {
	if err := a.DMS.RevokeWebDAVCredential(r.Context(), subject(r), r.PathValue("id")); err != nil {
		a.failure(w, r, err)
		return
	}
	a.Logger.InfoContext(r.Context(), "webdav credential revoked", "credential_id", r.PathValue("id"), "subject", subject(r), "request_id", r.Context().Value(requestKey{}))
	w.WriteHeader(204)
}
