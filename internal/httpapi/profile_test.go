package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/dal"
	"papergo/internal/dal/daltest"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"reflect"
	"testing"
)

func TestAccessTokenPreservesOIDCProfileAndMemberships(t *testing.T) {
	h, s, _ := setup(t)
	ctx := t.Context()
	d := s.Directory()
	u := identity.UserIdentity{Subject: "bob", Name: "Bob Builder", Email: "bob@example.com", Groups: []string{"developers"}, SyncGroups: true}
	if err := d.SyncUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	credential, err := d.CreateAccessToken(ctx, "bob", identity.AccessTokenInput{Name: "Reader", Scopes: []string{"item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	hashBefore := daltest.Values(t, dal.NewStore(d.DB), "identity_users", "sync_hash", daltest.Row{"id": "bob"})
	for range 2 {
		r := request(h, "GET", "/v1/identity/me", "", credential.Secret, "", "")
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
		var me identity.MeProfile
		if err := json.Unmarshal(r.Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if me.DisplayName != u.Name || me.Email != u.Email || len(me.Groups) != 1 || me.Groups[0] != "developers" {
			t.Fatalf("personal token changed OIDC identity: %+v", me)
		}
	}
	if hashAfter := daltest.Values(t, dal.NewStore(d.DB), "identity_users", "sync_hash", daltest.Row{"id": "bob"}); !reflect.DeepEqual(hashAfter, hashBefore) {
		t.Fatal("personal token invalidated the OIDC sync hash")
	}
}

func TestRESTProfilesAndAccountDeletion(t *testing.T) {
	h, s, _ := setup(t)
	ctx := context.Background()
	r := request(h, "POST", "/v1/identity/users", `{"subject":"profile-api","display_name":"API User","email":"api@example.com"}`, testToken, "", "application/json")
	if r.Code != 201 {
		t.Fatal(r.Code, r.Body.String())
	}
	var user identity.UserProfile
	if err := json.Unmarshal(r.Body.Bytes(), &user); err != nil || user.DisplayName != "API User" {
		t.Fatal(user, err)
	}
	r = request(h, "GET", "/v1/identity/me", "", testToken, "", "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "PATCH", "/v1/identity/me", `{"display_name":"Alice"}`, testToken, "", "application/json")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	credential, err := s.Directory().CreateAccessToken(ctx, "profile-api", identity.AccessTokenInput{Name: "Account", Scopes: []string{"item.read"}})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(ctx, "profile-api", "", dms.CreateResource{Kind: "workspace", Name: "Owned"})
	if err != nil {
		t.Fatal(err)
	}
	r = request(h, "DELETE", "/v1/identity/users/profile-api", "", testToken, "", "")
	if r.Code != 409 {
		t.Fatal("sole owner deleted", r.Code, r.Body.String())
	}
	if err = s.SetWorkspaceMember(ctx, "profile-api", w.ID, dms.WorkspaceMember{Subject: "alice", Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	r = request(h, "DELETE", "/v1/identity/users/profile-api", "", testToken, "", "")
	if r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/users/profile-api", "", testToken, "", "")
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err = json.Unmarshal(r.Body.Bytes(), &user); err != nil || user.DisplayName != "Deleted user" || user.Email != "" || user.Active {
		t.Fatal(user, err)
	}
	r = request(h, "PUT", "/v1/identity/users/profile-api/status", `{"active":true}`, testToken, "", "application/json")
	if r.Code != 404 {
		t.Fatal("deleted account revived", r.Code, r.Body.String())
	}
	r = request(h, "GET", "/v1/identity/me", "", credential.Secret, "", "")
	if r.Code != 401 {
		t.Fatal("deleted personal token admitted", r.Code, r.Body.String())
	}
	deletedHandler := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "profile-api"}, Logger: slog.Default()}).Handler()
	r = request(deletedHandler, "GET", "/v1/identity/me", "", testToken, "", "")
	if r.Code != 403 {
		t.Fatal("deleted bearer still admitted", r.Code, r.Body.String())
	}
	if count := daltest.Count(t, s.DAL, "grants", daltest.Row{"principal_type": "user", "principal_id": user.Subject}); count != 0 {
		t.Fatal(count)
	}
}
