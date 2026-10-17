package httpapi

import (
	"net/http"
	"papergo/internal/auth"
)

func (a *API) localLogin(local auth.Local) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !a.decode(w, r, &in) {
			return
		}
		value, _, err := local.Login(r.Context(), in.Username, in.Password)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="PaperGo", charset="UTF-8"`)
			a.problem(w, r, 401, "unauthorized", "valid authentication is required")
			return
		}
		http.SetCookie(w, a.localSessionCookie(r, value, 12*60*60))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) localLogout(local auth.Local) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(auth.SessionCookie); err == nil {
			if err = local.Logout(r.Context(), cookie.Value); err != nil {
				a.failure(w, r, err)
				return
			}
		}
		http.SetCookie(w, a.localSessionCookie(r, "", -1))
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *API) localSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil || (a.PublicURL != nil && a.PublicURL.Scheme == "https")
	return &http.Cookie{Name: auth.SessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}
