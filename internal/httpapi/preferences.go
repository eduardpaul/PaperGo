package httpapi

import (
	"net/http"
	"strconv"

	"papergo/internal/identity"
)

func (a *API) registerPreferences(m *http.ServeMux) {
	for _, endpoint := range []struct {
		path         string
		organization bool
	}{{"/v1/identity/me/preferences", false}, {"/v1/organization/preferences", true}} {
		organization := endpoint.organization
		m.HandleFunc("GET "+endpoint.path, func(w http.ResponseWriter, r *http.Request) {
			out, err := a.DMS.Directory().Preferences(r.Context(), subject(r), organization)
			if err != nil {
				a.failure(w, r, err)
				return
			}
			etag(w, out.Version)
			respond(w, 200, out)
		})
		m.HandleFunc("PATCH "+endpoint.path, func(w http.ResponseWriter, r *http.Request) {
			match := r.Header.Get("If-Match")
			if match == "" {
				a.problem(w, r, 428, "precondition_required", "If-Match is required")
				return
			}
			value, err := strconv.Unquote(match)
			if err != nil {
				a.problem(w, r, 400, "invalid_precondition", "If-Match must be a quoted nonnegative version")
				return
			}
			version, err := strconv.Atoi(value)
			if err != nil || version < 0 {
				a.problem(w, r, 400, "invalid_precondition", "If-Match must be a quoted nonnegative version")
				return
			}
			var patch map[string]*string
			if !a.decode(w, r, &patch) {
				return
			}
			var out identity.Preferences
			out, err = a.DMS.Directory().PatchPreferences(r.Context(), subject(r), organization, version, patch)
			if err != nil {
				a.failure(w, r, err)
				return
			}
			etag(w, out.Version)
			respond(w, 200, out)
		})
	}
}
