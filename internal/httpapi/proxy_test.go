package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"papergo/internal/auth"
	"papergo/internal/identity"
	"strings"
	"testing"
)

func TestProxyIdentityPreservesProfilesAndRejectsCredentialBypasses(t *testing.T) {
	_, s, _ := setup(t)
	ctx := t.Context()
	name, email := "Bob Profile", "bob@example.com"
	if _, err := s.Directory().UpdateUser(ctx, "organization-admin", "bob", identity.UserUpdate{DisplayName: &name, Email: &email}); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 64)
	proxy, err := auth.NewProxy(secret, []netip.Prefix{netip.MustParsePrefix("172.29.242.2/32")}, "https://docs.example.com")
	if err != nil {
		t.Fatal(err)
	}
	h := (&API{DMS: s, Auth: proxy, Logger: slog.Default()}).Handler()
	config := request(h, "GET", "/auth/config", "", "", "", "")
	if config.Code != 200 || strings.TrimSpace(config.Body.String()) != `{"mode":"proxy"}` || config.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("public proxy capability discovery", config.Code, config.Body.String(), config.Header())
	}
	do := func(method, path, body, subject, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "172.29.242.2:1234"
		r.Header.Set(auth.ProxySubjectHeader, subject)
		r.Header.Set(auth.ProxySecretHeader, secret)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	me := do("GET", "/v1/identity/me", "", "bob", "")
	var profile identity.MeProfile
	if me.Code != 200 || json.Unmarshal(me.Body.Bytes(), &profile) != nil || profile.DisplayName != name || profile.Email != email {
		t.Fatal(me.Code, me.Body.String())
	}
	for _, path := range []string{"/", "/admin/users", "/settings/webdav"} {
		res := do("GET", path, "", "bob", "")
		if res.Code != 200 || !strings.Contains(res.Body.String(), `<div id="root">`) {
			t.Fatal("embedded proxy frontend", path, res.Code)
		}
	}
	mutation := httptest.NewRequest("POST", "/v1/workspaces", strings.NewReader(`{"name":"Browser proxy workspace"}`))
	mutation.RemoteAddr = "172.29.242.2:1234"
	mutation.Header.Set(auth.ProxySubjectHeader, "bob")
	mutation.Header.Set(auth.ProxySecretHeader, secret)
	mutation.Header.Set("Content-Type", "application/json")
	mutation.Header.Set("Origin", "https://docs.example.com")
	mutation.Header.Set("Sec-Fetch-Site", "same-origin")
	accepted := httptest.NewRecorder()
	h.ServeHTTP(accepted, mutation)
	if accepted.Code != 201 {
		t.Fatal("same-origin proxy browser write", accepted.Code, accepted.Body.String())
	}
	mutation.Header.Set("Origin", "https://attacker.example")
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, mutation)
	if rejected.Code != 401 {
		t.Fatal("cross-origin browser write", rejected.Code)
	}
	if r := do("GET", "/v1/identity/me", "", "proxy-member", ""); r.Code != 200 {
		t.Fatal("verified username was not enrolled", r.Code, r.Body.String())
	}
	if r := do("GET", "/v1/identity/roles", "", "proxy-member", ""); r.Code != 403 {
		t.Fatal("proxy username bypassed local role permissions", r.Code)
	}
	if _, err := s.Directory().SetUserActive(ctx, "organization-admin", "proxy-member", false); err != nil {
		t.Fatal(err)
	}
	if r := do("GET", "/v1/identity/me", "", "proxy-member", ""); r.Code != 403 {
		t.Fatal("proxy request reactivated a disabled user", r.Code)
	}
	for _, path := range []string{"/v1/identity/me/tokens"} {
		if r := do("GET", path, "", "bob", ""); r.Code != 404 {
			t.Fatal(path, r.Code)
		}
	}
	token, err := s.Directory().CreateAccessToken(ctx, "bob", identity.AccessTokenInput{Name: "Bypass", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if r := do("GET", "/v1/identity/me", "", "bob", token.Secret); r.Code != 401 {
		t.Fatal("personal credential bypassed proxy policy", r.Code)
	}
	batch := do("POST", "/v1/$batch", `{"requests":[{"id":"me","method":"GET","url":"identity/me"}]}`, "bob", "")
	if batch.Code != 200 || !strings.Contains(batch.Body.String(), `"subject":"bob"`) || !strings.Contains(batch.Body.String(), `"status":200`) {
		t.Fatal("proxy batch lost verified identity", batch.Code, batch.Body.String())
	}
	for _, header := range []string{auth.ProxySubjectHeader, auth.ProxySecretHeader} {
		body := `{"requests":[{"id":"me","method":"GET","url":"identity/me","headers":{"` + header + `":"organization-admin"}}]}`
		if r := do("POST", "/v1/$batch", body, "bob", ""); r.Code != 400 {
			t.Fatal("batch overrode trusted identity", header, r.Code)
		}
	}
	r := httptest.NewRequest("GET", "/v1/identity/me", nil)
	r.RemoteAddr = "172.29.242.1:1234"
	r.Header.Set(auth.ProxySubjectHeader, "organization-admin")
	r.Header.Set(auth.ProxySecretHeader, secret)
	r.Header.Set("X-Forwarded-For", "172.29.242.2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("untrusted peer spoofed proxy", w.Code)
	}
}
