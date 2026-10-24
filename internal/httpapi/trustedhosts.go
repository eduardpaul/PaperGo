package httpapi

import "net/http"

func (a *API) registerTrustedHosts(api *router) {
	api.HandleFunc("GET /v1/organization/settings/trusted-hosts", a.trustedHosts)
	api.HandleFunc("PUT /v1/organization/settings/trusted-hosts", a.setTrustedHosts)
	api.HandleFunc("GET /v1/organization/settings/trusted-hosts/usage", a.trustedHostUsage)
}

func (a *API) trustedHosts(w http.ResponseWriter, r *http.Request) {
	out, err := a.dms(r).TrustedHosts(r.Context())
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) setTrustedHosts(w http.ResponseWriter, r *http.Request) {
	version, ok := a.versionAtLeast(w, r, 0)
	if !ok {
		return
	}
	var in struct {
		Hosts []string `json:"hosts"`
	}
	if !a.decode(w, r, &in) {
		return
	}
	if in.Hosts == nil {
		a.problem(w, r, 422, "validation_failed", "hosts is required")
		return
	}
	out, err := a.dms(r).SetTrustedHosts(r.Context(), subject(r), int64(version), in.Hosts)
	if err != nil {
		a.failure(w, r, err)
		return
	}
	etag(w, int(out.Version))
	respond(w, r, 200, out)
}

func (a *API) trustedHostUsage(w http.ResponseWriter, r *http.Request) {
	out, err := a.dms(r).TrustedHostUsage(r.Context(), subject(r))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	respond(w, r, 200, map[string]any{"value": out})
}
