package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"papergo/internal/dal/daltest"
	"papergo/internal/dal/directory"
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
	var token identity.CreatedAccessToken
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &token) != nil || !strings.HasPrefix(token.Secret, identity.AccessTokenPrefix) || token.Token.ExpiresAt.Before(time.Now()) {
		t.Fatal(r.Code, r.Body.String())
	}
	if stored := daltest.Values(t, s.DAL, "access_tokens", "secret_hash", daltest.Row{"id": token.Token.ID}); len(stored) != 1 || strings.Contains(fmt.Sprint(stored[0]), token.Secret) || len(fmt.Sprint(stored[0])) != 64 {
		t.Fatal("token secret storage", stored)
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
	var listed []identity.AccessToken
	if json.Unmarshal(r.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].LastUsedAt == nil {
		t.Fatal(r.Body.String())
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, true); err != nil {
		t.Fatal(err)
	}
	r = request(h, "GET", "/v1/resources/"+i.ID, "", token.Secret, "", "")
	if r.Code != 403 {
		t.Fatal("role removal did not affect token", r.Code, r.Body.String())
	}
	if err = s.Directory().SetAssignment(ctx, "organization-admin", identity.MemberRole, identity.Principal{Type: directory.PrincipalUser, ID: "alice"}, false); err != nil {
		t.Fatal(err)
	}
	if err = s.Directory().RevokeAccessToken(ctx, "bob", token.Token.ID); err != identity.ErrNotFound {
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
	expired, err := s.Directory().CreateAccessToken(ctx, "alice", identity.AccessTokenInput{Name: "Expired", Scopes: []string{"list.read"}})
	if err != nil {
		t.Fatal(err)
	}
	daltest.Update(t, s.DAL, "access_tokens", daltest.Row{"expires_at": time.Now().UTC().Add(-time.Hour)}, daltest.Row{"id": expired.Token.ID})
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
