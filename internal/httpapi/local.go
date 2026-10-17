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
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 12 * 60 * 60})
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
		http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
		w.WriteHeader(http.StatusNoContent)
	}
}
