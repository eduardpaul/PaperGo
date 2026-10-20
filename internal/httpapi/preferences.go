package httpapi

import (
	"net/http"

	"papergo/internal/identity"
)

func (a *API) registerPreferences(m *router) {
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
			respond(w, r, 200, out)
		})
		m.HandleFunc("PATCH "+endpoint.path, func(w http.ResponseWriter, r *http.Request) {
			version, ok := a.versionAtLeast(w, r, 0)
			if !ok {
				return
			}
			var patch map[string]*string
			if !a.decode(w, r, &patch) {
				return
			}
			var out identity.Preferences
			out, err := a.DMS.Directory().PatchPreferences(r.Context(), subject(r), organization, version, patch)
			if err != nil {
				a.failure(w, r, err)
				return
			}
			etag(w, out.Version)
			respond(w, r, 200, out)
		})
	}
}
