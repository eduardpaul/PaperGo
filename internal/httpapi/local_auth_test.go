package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"papergo/internal/auth"
	"papergo/internal/database"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"papergo/internal/testutil/searchstore"
)

func TestLocalAdminBasicAndSession(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data", "papergo.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.EnsureSchema(ctx); err != nil {
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
	var stored string
	if err = db.SQL.QueryRowContext(ctx, "SELECT password_hash FROM identity_users WHERE id='admin'").Scan(&stored); err != nil || stored != hash {
		t.Fatal("second start reset the password", stored, err)
	}
	service := dms.NewService(db.SQL)
	index := searchstore.Open(t, filepath.Join(t.TempDir(), "search.db"), []string{"english"})
	service.SetSearch(index)
	handler := (&API{DMS: service, Auth: auth.Local{DB: db.SQL}, Logger: slog.Default(), Ready: db.SQL.PingContext}).Handler()
	config := request(handler, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"local"}` {
		t.Fatal(config.Code, config.Body.String())
	}
	basic := httptest.NewRequest(http.MethodGet, "/v1/search/status", nil)
	basic.SetBasicAuth("admin", "admin")
	basicRec := httptest.NewRecorder()
	handler.ServeHTTP(basicRec, basic)
	if basicRec.Code != 200 || !strings.Contains(basicRec.Body.String(), `"store":"sqlite"`) {
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
	follow := httptest.NewRequest(http.MethodGet, "/v1/search/status", nil)
	follow.AddCookie(cookie)
	followRec := httptest.NewRecorder()
	handler.ServeHTTP(followRec, follow)
	if followRec.Code != 200 || !strings.Contains(followRec.Body.String(), `"store":"sqlite"`) {
		t.Fatal("session", followRec.Code, followRec.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/v1/search/status", nil)
	denied.SetBasicAuth("admin", "wrong")
	deniedRec := httptest.NewRecorder()
	handler.ServeHTTP(deniedRec, denied)
	if deniedRec.Code != 401 {
		t.Fatal("wrong password", deniedRec.Code, deniedRec.Body.String())
	}
	t.Logf("basic admin:admin and session cookie succeeded on /v1/search/status; second password was not stored")
}
