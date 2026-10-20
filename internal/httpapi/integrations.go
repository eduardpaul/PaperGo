package httpapi

import (
	"net/http"
	"papergo/internal/dms"
)

func (a *API) registerIntegrations(m *router) {
	m.HandleFunc("GET /v1/resources/{id}/integrations", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).ListIntegrations(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/integrations", foundationMutation(a, 201, false, func(r *http.Request, in dms.IntegrationInput, _ int) (any, error) {
		return a.dms(r).CreateIntegration(r.Context(), subject(r), r.PathValue("id"), in)
	}))
	m.HandleFunc("PATCH /v1/resources/{id}/integrations/{integrationID}", foundationMutation(a, 200, true, func(r *http.Request, in dms.UpdateIntegration, version int) (any, error) {
		return a.dms(r).UpdateIntegration(r.Context(), subject(r), r.PathValue("id"), r.PathValue("integrationID"), version, in)
	}))
	m.HandleFunc("DELETE /v1/resources/{id}/integrations/{integrationID}", func(w http.ResponseWriter, r *http.Request) {
		version, ok := a.version(w, r)
		if !ok {
			return
		}
		if err := a.dms(r).RemoveIntegration(r.Context(), subject(r), r.PathValue("id"), r.PathValue("integrationID"), version); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /v1/resources/{id}/integrations/{integrationID}/refresh", a.idempotent(func(w http.ResponseWriter, r *http.Request) {
		op, err := a.dms(r).RefreshIntegration(r.Context(), subject(r), r.PathValue("id"), r.PathValue("integrationID"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		Accepted(w, r, op)
	}))
	m.HandleFunc("GET /v1/items/{id}/integration", foundationRead(a, func(r *http.Request) (any, error) {
		return a.dms(r).ItemIntegration(r.Context(), subject(r), r.PathValue("id"))
	}))
	m.HandleFunc("POST /v1/resources/{id}/calendar/occurrences", foundationMutation(a, 200, false, func(r *http.Request, in dms.CalendarQuery, _ int) (any, error) {
		return a.dms(r).CalendarOccurrences(r.Context(), subject(r), r.PathValue("id"), in.From, in.To, in.Surface, in.Query)
	}))
	m.HandleFunc("GET /v1/resources/{id}/calendar/export", func(w http.ResponseWriter, r *http.Request) {
		body, err := a.dms(r).CalendarExport(r.Context(), subject(r), r.PathValue("id"), r.URL.Query().Get("surface"))
		if err != nil {
			a.failure(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="calendar.ics"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	})
}
