package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
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
	body := `{"name":"Calendar integration","scopes":["workspace.read","item.read"],"expires_in_days":0}`
	if got := request(h, "POST", "/v1/identity/tokens", body, testToken, "", "application/json"); got.Code != 403 {
		t.Fatal(got.Code, got.Body.String())
	}
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	got := request(h, "POST", "/v1/identity/tokens", body, testToken, "", "application/json")
	var created identity.CreatedAccessToken
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
	var listed []identity.AccessToken
	if got.Code != 200 || json.Unmarshal(got.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].LastUsedAt == nil || strings.Contains(got.Body.String(), created.Secret) || strings.Contains(got.Body.String(), "secret_hash") {
		t.Fatal(got.Code, got.Body.String())
	}
	// Persisted operations restore an ID and cap, without trusting a cached kind flag.
	credential := identity.AccessTokenIdentity{ID: created.Token.ID, Subject: created.Token.Subject, Scopes: created.Token.Scopes}
	queued := identity.WithAccessToken(ctx, credential)
	if err = identity.RequireScope(queued, dal.NewStore(s.Directory().DB), created.Token.Subject, "item.read"); err != nil {
		t.Fatal("queued credential lost integration grant", err)
	}
	got = request(h, "DELETE", "/v1/identity/tokens/"+created.Token.ID, "", testToken, "", "")
	if got.Code != 204 {
		t.Fatal(got.Code, got.Body.String())
	}
	if err = identity.RequireCredential(queued, dal.NewStore(s.Directory().DB), created.Token.Subject); err != identity.ErrInvalidAccessToken {
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

// Proxy deployments accept both kinds of access token: integration tokens
// for services and personal tokens, such as an AI assistant's.
func TestProxyAcceptsIntegrationAndPersonalBearer(t *testing.T) {
	_, s, _ := setup(t)
	ctx := context.Background()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	integration, err := s.Directory().CreateIntegrationToken(ctx, "alice", identity.AccessTokenInput{Name: "Service", Scopes: []string{"item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	personal, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Personal", Scopes: []string{"item.read"}})
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
	}{{integration.Secret, 200}, {personal.Secret, 200}, {"pgo_" + strings.Repeat("x", 43), 401}} {
		got := request(h, http.MethodGet, "/v1/identity/me", "", tc.secret, "", "")
		if got.Code != tc.status {
			t.Fatal(got.Code, got.Body.String())
		}
	}
}

func TestIntegrationTokenExpiryAndDisabledIdentityAreRevalidated(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"expired", "disabled"} {
		token, err := s.Directory().CreateIntegrationToken(ctx, "alice", identity.AccessTokenInput{Name: mode, Scopes: []string{"item.read"}})
		if err != nil {
			t.Fatal(err)
		}
		queued := identity.WithAccessToken(ctx, identity.AccessTokenIdentity{ID: token.Token.ID, Subject: token.Token.Subject, Scopes: token.Token.Scopes})
		if mode == "expired" {
			_, err = daltest.TryUpdate(s.DAL, "access_tokens", daltest.Row{"expires_at": time.Now().UTC().Add(-time.Hour)}, daltest.Row{"id": token.Token.ID})
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
		if err = identity.RequireScope(queued, dal.NewStore(s.Directory().DB), token.Token.Subject, "item.read"); err == nil {
			t.Fatal(mode, "retained live scope")
		}
	}
}

func TestOrganizationManagersCanReadTokenScopeCatalogWithoutRoleAdministration(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	if err := s.Directory().SetAssignment(ctx, "organization-admin", identity.AdministratorRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	limited, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Organization manager", Scopes: []string{"organization.manage", "item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	got := request(h, "GET", "/v1/identity/scopes", "", limited.Secret, "", "")
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	got = request(h, "POST", "/v1/identity/tokens", `{"name":"Escalate","scopes":["item.write"]}`, limited.Secret, "", "application/json")
	if got.Code != 422 {
		t.Fatal("manager exceeded credential scope", got.Code, got.Body.String())
	}
}
