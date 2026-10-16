package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
)

func TestAdminIntegrationTokensHaveServiceIdentityScopedAccessAndRevocation(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	body := `{"name":"Calendar integration","scopes":["workspace.read","list.read"],"expires_in_days":0}`
	if got := request(h, "POST", "/v1/identity/tokens", body, testToken, "", "application/json"); got.Code != 403 {
		t.Fatal(got.Code, got.Body.String())
	}
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	got := request(h, "POST", "/v1/identity/tokens", body, testToken, "", "application/json")
	var created identity.CreatedRESTToken
	if got.Code != 201 || json.Unmarshal(got.Body.Bytes(), &created) != nil || !created.Token.Integration || created.Token.Subject == "alice" || created.Token.ExpiresAt != nil || !strings.HasPrefix(created.Secret, "pgo_") {
		t.Fatal(got.Code, got.Body.String())
	}
	if err := s.Directory().EnsureUser(ctx, created.Token.Subject); err != identity.ErrForbidden {
		t.Fatal("service identity accepted proxy login", err)
	}
	if err := s.Directory().SyncUser(ctx, identity.UserIdentity{Subject: created.Token.Subject}); err != identity.ErrForbidden {
		t.Fatal("service identity accepted provider login", err)
	}
	workspace, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Token test"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Create(ctx, "alice", workspace.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	got = request(h, "GET", "/v1/resources/"+list.ID, "", created.Secret, "", "")
	if got.Code != 403 {
		t.Fatal("token bypassed ACL", got.Code, got.Body.String())
	}
	if err = s.SetWorkspaceMember(ctx, "alice", workspace.ID, dms.WorkspaceMember{Subject: created.Token.Subject, Role: "member"}); err != nil {
		t.Fatal(err)
	}
	got = request(h, "GET", "/v1/resources/"+list.ID, "", created.Secret, "", "")
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	got = request(h, "PATCH", "/v1/resources/"+list.ID, `{"name":"Denied"}`, created.Secret, `"1"`, "application/json")
	if got.Code != 403 {
		t.Fatal("scope cap ignored", got.Code, got.Body.String())
	}
	got = request(h, "GET", "/v1/identity/me", "", created.Secret, "", "")
	var profile identity.MeProfile
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &profile) != nil || profile.Subject != created.Token.Subject || len(profile.Scopes) != 2 {
		t.Fatal(got.Code, got.Body.String())
	}
	got = request(h, "GET", "/v1/identity/tokens", "", created.Secret, "", "")
	if got.Code != 403 {
		t.Fatal("non-admin listed organization tokens", got.Code)
	}
	got = request(h, "GET", "/v1/identity/tokens", "", testToken, "", "")
	var listed []identity.RESTToken
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].LastUsedAt == nil || strings.Contains(got.Body.String(), created.Secret) || strings.Contains(got.Body.String(), "secret_hash") {
		t.Fatal(got.Code, got.Body.String())
	}
	// Persisted operations restore an ID and cap, without trusting a cached kind flag.
	credential := identity.RESTTokenIdentity{ID: created.Token.ID, Subject: created.Token.Subject, Scopes: created.Token.Scopes}
	queued := identity.WithRESTToken(ctx, credential)
	if err = identity.RequireScope(queued, s.Directory().DB, created.Token.Subject, "list.read"); err != nil {
		t.Fatal("queued credential lost integration grant", err)
	}
	got = request(h, "DELETE", "/v1/identity/tokens/"+created.Token.ID, "", testToken, "", "")
	if got.Code != 204 {
		t.Fatal(got.Code, got.Body.String())
	}
	if err = identity.RequireCredential(queued, s.Directory().DB, created.Token.Subject); err != identity.ErrInvalidRESTToken {
		t.Fatal("queued credential not revoked", err)
	}
	got = request(h, "GET", "/v1/identity/me", "", created.Secret, "", "")
	if got.Code != 401 {
		t.Fatal("revoked credential authenticated", got.Code)
	}
	got = request(h, "GET", "/v1/identity/tokens", "", testToken, "", "")
	if json.Unmarshal(got.Body.Bytes(), &listed) != nil || listed[0].RevokedAt == nil {
		t.Fatal("revoked audit row missing", got.Body.String())
	}
}

func TestProxyAcceptsIntegrationBearerAndRejectsPersonalBearer(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	integration, err := s.Directory().CreateIntegrationToken(ctx, "alice", identity.RESTTokenInput{Name: "Service", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := s.Directory().CreateRESTToken(ctx, "alice", identity.RESTTokenInput{Name: "Personal", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := auth.NewProxy(strings.Repeat("a", 64), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, "https://papergo.example")
	if err != nil {
		t.Fatal(err)
	}
	h := (&API{DMS: s, Auth: proxy, Logger: slog.Default()}).Handler()
	for _, tc := range []struct {
		secret string
		status int
	}{{integration.Secret, 200}, {personal.Secret, 401}} {
		got := request(h, http.MethodGet, "/v1/identity/me", "", tc.secret, "", "")
		if got.Code != tc.status {
			t.Fatal(got.Code, got.Body.String())
		}
	}
}

func TestIntegrationTokenExpiryAndDisabledIdentityAreRevalidated(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"expired", "disabled"} {
		token, err := s.Directory().CreateIntegrationToken(ctx, "alice", identity.RESTTokenInput{Name: mode, Scopes: []string{"list.read"}})
		if err != nil {
			t.Fatal(err)
		}
		queued := identity.WithRESTToken(ctx, identity.RESTTokenIdentity{ID: token.Token.ID, Subject: token.Token.Subject, Scopes: token.Token.Scopes})
		if mode == "expired" {
			_, err = s.Directory().DB.ExecContext(ctx, "UPDATE identity_rest_tokens SET expires_at=? WHERE id=?", time.Now().UTC().Add(-time.Hour), token.Token.ID)
		} else {
			_, err = s.Directory().SetUserActive(ctx, "alice", token.Token.Subject, false)
		}
		if err != nil {
			t.Fatal(err)
		}
		got := request(h, "GET", "/v1/identity/me", "", token.Secret, "", "")
		if got.Code != 401 {
			t.Fatal(mode, got.Code, got.Body.String())
		}
		if err = identity.RequireScope(queued, s.Directory().DB, token.Token.Subject, "list.read"); err == nil {
			t.Fatal(mode, "retained live scope")
		}
	}
}
