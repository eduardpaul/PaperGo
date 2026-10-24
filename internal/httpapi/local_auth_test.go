package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/dal/database"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestLocalAdminBasicAndSession(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "papergo.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Upgrade(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	directory := identity.Directory{DB: db.SQL}
	exists, err := identity.HasAdministrator(ctx, db.SQL)
	if err != nil || exists {
		t.Fatal("empty database already has an administrator", exists, err)
	}
	if err = directory.Initialize(ctx, "admin", ""); err != nil {
		t.Fatal(err)
	}
	if err = directory.SetInitialPasswordHash(ctx, "admin", ""); err == nil {
		t.Fatal("missing password was accepted")
	}
	hash, err := auth.HashPassword("admin")
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetInitialPasswordHash(ctx, "admin", hash); err != nil {
		t.Fatal(err)
	}
	other, err := auth.HashPassword("changed-password")
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetInitialPasswordHash(ctx, "admin", other); err != nil {
		t.Fatal(err)
	}
	if stored := daltest.Values(t, db.DAL, "identity_users", "password_hash", daltest.Row{"id": "admin"}); len(stored) != 1 || stored[0] != hash {
		t.Fatal("second start reset the password", stored)
	}
	service := dms.NewService(db.SQL)
	handler := (&API{DMS: service, Auth: auth.Local{DAL: db.DAL}, Logger: slog.Default(), Ready: db.SQL.PingContext}).Handler()
	config := request(handler, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"local","features":{"comments":true}}` {
		t.Fatal(config.Code, config.Body.String())
	}
	basic := httptest.NewRequest(http.MethodGet, "/v1/storage/status", nil)
	basic.SetBasicAuth("admin", "admin")
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != 200 || !strings.Contains(basicRec.Body.String(), `"database_bytes"`) {
		t.Fatal("basic admin", basicRec.Code, basicRec.Body.String())
	}
	login := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`{"username":"admin","password":"admin"}`))
	login.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, login)
	if loginRec.Code != 204 || loginRec.Result().Cookies() == nil {
		t.Fatal("login", loginRec.Code, loginRec.Header())
	}
	var cookie *http.Cookie
	for _, item := range loginRec.Result().Cookies() {
		if item.Name == auth.SessionCookie && item.HttpOnly {
			cookie = item
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("session cookie missing", loginRec.Header())
	}
	follow := httptest.NewRequest(http.MethodGet, "/v1/storage/status", nil)
	follow.AddCookie(cookie)
	followRec := httptest.NewRecorder()
	handler.ServeHTTP(followRec, follow)
	if followRec.Code != 200 || !strings.Contains(followRec.Body.String(), `"database_bytes"`) {
		t.Fatal("session", followRec.Code, followRec.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/v1/storage/status", nil)
	denied.SetBasicAuth("admin", "wrong")
	deniedRec := httptest.NewRecorder()
	handler.ServeHTTP(deniedRec, denied)
	if deniedRec.Code != 401 {
		t.Fatal("wrong password", deniedRec.Code, deniedRec.Body.String())
	}
	t.Logf("basic admin:admin and session cookie succeeded on /v1/storage/status; second password was not stored")
}

func TestLocalAuthenticationPreservesManagedProfile(t *testing.T) {
	_, service, _ := setup(t)
	ctx := t.Context()
	directory := service.Directory()
	hash, err := auth.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetInitialPasswordHash(ctx, "alice", hash); err != nil {
		t.Fatal(err)
	}
	local := auth.Local{DAL: dal.NewStore(directory.DB)}
	handler := (&API{DMS: service, Auth: local, Logger: slog.Default()}).Handler()
	getMe := func(cookie string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/v1/identity/me", nil)
		if cookie == "" {
			req.SetBasicAuth("alice", "password")
		} else {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	getMe("")
	cookie, _, err := local.Login(ctx, "alice", "password")
	if err != nil {
		t.Fatal(err)
	}
	for _, credential := range []string{"", cookie} {
		name, email := "Local administrator", "admin@example.test"
		if _, err = directory.UpdateUser(ctx, "organization-admin", "alice", identity.UserUpdate{DisplayName: &name, Email: &email}); err != nil {
			t.Fatal(err)
		}
		getMe(credential)
		profile, err := directory.Me(ctx, "alice")
		if err != nil || profile.DisplayName != name || profile.Email != email {
			t.Fatalf("local authentication changed the managed profile: %+v, %v", profile, err)
		}
	}
}

func TestLocalSessionCookieSecurityAndLogout(t *testing.T) {
	_, service, _ := setup(t)
	directory := service.Directory()
	hash, err := auth.HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if err = directory.SetInitialPasswordHash(t.Context(), "alice", hash); err != nil {
		t.Fatal(err)
	}
	local := auth.Local{DAL: dal.NewStore(directory.DB)}
	for _, test := range []struct {
		name, origin, publicURL string
		secure                  bool
	}{
		{"localhost HTTP", "http://localhost:8080", "", false},
		{"public HTTP", "http://localhost:8080", "http://localhost:8080", false},
		{"direct HTTPS", "https://docs.example.test", "", true},
		{"TLS proxy", "http://localhost:8080", "https://docs.example.test", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var publicURL *url.URL
			if test.publicURL != "" {
				publicURL, err = url.Parse(test.publicURL)
				if err != nil {
					t.Fatal(err)
				}
			}
			handler := (&API{DMS: service, Auth: local, Logger: slog.Default(), PublicURL: publicURL}).Handler()
			login := httptest.NewRequest(http.MethodPost, test.origin+"/auth/login", strings.NewReader(`{"username":"alice","password":"password"}`))
			login.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, login)
			cookies := rec.Result().Cookies()
			if rec.Code != http.StatusNoContent || len(cookies) != 1 {
				t.Fatal(rec.Code, rec.Header())
			}
			cookie := cookies[0]
			if cookie.Name != auth.SessionCookie || !cookie.HttpOnly || cookie.Secure != test.secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge != 12*60*60 {
				t.Fatalf("unexpected session cookie: %+v", cookie)
			}
			logout := httptest.NewRequest(http.MethodPost, test.origin+"/auth/logout", nil)
			logout.AddCookie(cookie)
			rec = httptest.NewRecorder()
			handler.ServeHTTP(rec, logout)
			cookies = rec.Result().Cookies()
			if rec.Code != http.StatusNoContent || len(cookies) != 1 || cookies[0].MaxAge != -1 || cookies[0].Secure != test.secure {
				t.Fatal("logout did not expire the session cookie", rec.Code, rec.Header())
			}
			if _, err = local.Session(t.Context(), cookie.Value); err == nil {
				t.Fatal("logged-out session still authenticates")
			}
		})
	}
}
