package httpapi

import "net/http"

func (a *API) delta(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	out, err := a.dms(r).Delta(r.Context(), subject(r), r.PathValue("id"), q.Get("$deltatoken"), q.Get("$skiptoken"), queryInt(r, "$top"))
	if err != nil {
		a.failure(w, r, err)
		return
	}
	values := map[string]any{"value": out.Data}
	link := func(key, token string) string {
		u := *r.URL
		query := u.Query()
		query.Del("$deltatoken")
		query.Del("$skiptoken")
		query.Set(key, token)
		u.RawQuery = query.Encode()
		if u.Scheme == "" {
			u.Scheme = "http"
			if r.TLS != nil {
				u.Scheme = "https"
			}
		}
		if u.Host == "" {
			u.Host = r.Host
		}
		return u.String()
	}
	if out.NextToken != "" {
		values["@odata.nextLink"] = link("$skiptoken", out.NextToken)
	}
	if out.DeltaToken != "" {
		values["@odata.deltaLink"] = link("$deltatoken", out.DeltaToken)
	}
	respond(w, r, 200, values)
}
