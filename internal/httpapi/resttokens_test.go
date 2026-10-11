package httpapi

import (
	"context"
	"encoding/json"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"strings"
	"testing"
	"time"
)

func TestRESTPersonalTokensEnforceScopesExpiryAndRevocation(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	w, err := s.Create(ctx, "alice", "", dms.CreateResource{Kind: "workspace", Name: "Tokens"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.Create(ctx, "alice", w.ID, dms.CreateResource{Kind: "list", Name: "Records"})
	if err != nil {
		t.Fatal(err)
	}
	i, err := s.Create(ctx, "alice", l.ID, dms.CreateResource{Kind: "item", Name: "Record"})
	if err != nil {
		t.Fatal(err)
	}
	r := request(h, "POST", "/v1/identity/me/tokens", `{"name":"Read only","scopes":["list.read"],"expires_in_days":1}`, testToken, "", "application/json")
	var token identity.CreatedRESTToken
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &token) != nil || !strings.HasPrefix(token.Secret, identity.RESTTokenPrefix) || token.Token.ExpiresAt.Before(time.Now()) {
		t.Fatal(r.Code, r.Body.String())
	}
	var stored string
	if err = s.Directory().DB.QueryRowContext(ctx, "SELECT secret_hash FROM identity_rest_tokens WHERE id=?", token.Token.ID).Scan(&stored); err != nil || strings.Contains(stored, token.Secret) || len(stored) != 64 {
		t.Fatal("token secret storage", err)
	}
	r = request(h, "GET", "/v1/resources/"+i.ID, "", token.Secret, "", "")
	if r.Code != 200 {
		t.Fatal("read denied", r.Code, r.Body.String())
	}
	r = request(h, "PATCH", "/v1/resources/"+i.ID, `{"name":"Blocked"}`, token.Secret, `"1"`, "application/json")
	if r.Code != 403 {
		t.Fatal("read token wrote item", r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/roles", "", token.Secret, "", "")
	if r.Code != 403 {
		t.Fatal("scope cap ignored", r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/me", "", token.Secret, "", "")
	var me identity.MeProfile
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &me) != nil || len(me.Scopes) != 1 || me.Scopes[0] != "list.read" {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "POST", "/v1/identity/me/tokens", `{"name":"Escalate","scopes":["list.write"]}`, token.Secret, "", "application/json")
	if r.Code != 422 {
		t.Fatal("token escalated scopes", r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/me/tokens", "", testToken, "", "")
	if r.Code != 200 || strings.Contains(r.Body.String(), token.Secret) || strings.Contains(r.Body.String(), "secret_hash") {
		t.Fatal("secret exposed", r.Code, r.Body.String())
	}
	var listed []identity.RESTToken
	if json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].LastUsedAt == nil {
		t.Fatal(r.Body.String())
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: "user", ID: "alice"}, true); err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", "/v1/resources/"+i.ID, "", token.Secret, "", "")
	if r.Code != 403 {
		t.Fatal("role removal did not affect token", r.Code, r.Body.String())
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: "user", ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().RevokeRESTToken(ctx, "bob", token.Token.ID); err != identity.ErrNotFound {
		t.Fatal("another user revoked token", err)
	}
	r = request(h, "DELETE", "/v1/identity/me/tokens/"+token.Token.ID, "", testToken, "", "")
	if r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/resources/"+i.ID, "", token.Secret, "", "")
	if r.Code != 401 {
		t.Fatal("revoked token authenticated", r.Code, r.Body.String())
	}
	expired, err := s.Directory().CreateRESTToken(ctx, "alice", identity.RESTTokenInput{Name: "Expired", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Directory().DB.ExecContext(ctx, "UPDATE identity_rest_tokens SET expires_at=? WHERE id=?", time.Now().UTC().Add(-time.Hour), expired.Token.ID); err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", "/v1/resources/"+i.ID, "", expired.Secret, "", "")
	if r.Code != 401 {
		t.Fatal("expired token authenticated", r.Code, r.Body.String())
	}
	for _, body := range []string{`{"name":"Invalid","scopes":[]}`, `{"name":"Invalid","scopes":["list.read","list.read"]}`, `{"name":"Invalid","scopes":["organization.manage"]}`, `{"name":"Invalid","scopes":["list.read"],"expires_in_days":0}`} {
		r = request(h, "POST", "/v1/identity/me/tokens", body, testToken, "", "application/json")
		if r.Code != 422 {
			t.Fatal(body, r.Code, r.Body.String())
		}
	}
}
