package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) registerInboxes(m *router) {
	m.HandleFunc("GET /v1/identity/me/home", foundationRead(a, func(r *http.Request) (any, error) { return a.dms(r).Home(r.Context(), subject(r)) }))
	m.HandleFunc("GET /v1/identity/me/inboxes", foundationRead(a, func(r *http.Request) (any, error) { return a.dms(r).Inboxes(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/identity/me/inbox/documents", a.createPersonalInboxDocument)
	m.HandleFunc("GET /v1/identity/groups/{id}/inbox", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).GroupInbox(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("PUT /v1/identity/groups/{id}/inbox", foundationMutation(a, 200, false, func(r *http.Request, in dms.GroupInboxInput, _ int) (any, error) {
		return a.dms(r).SetGroupInbox(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("DELETE /v1/identity/groups/{id}/inbox", func(w http.ResponseWriter, r *http.Request) {
		if err := a.dms(r).RemoveGroupInbox(r.Context(), subject(r), r.PathValue("id")); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /v1/identity/groups/{id}/inbox/documents", a.createGroupInboxDocument)
}
