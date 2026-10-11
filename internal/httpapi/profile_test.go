package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"papergo/internal/auth"
	"papergo/internal/dms"
	"papergo/internal/identity"
	"testing"
)

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
	deletedHandler := (&API{DMS: s, Auth: auth.Development{Token: testToken, Subject: "profile-api"}, Logger: slog.Default()}).Handler()
	r = request(deletedHandler, "GET", "/v1/identity/me", "", testToken, "", "")
	if r.Code != 403 {
		t.Fatal("deleted bearer still admitted", r.Code, r.Body.String())
	}
	var count int
	if err = s.Directory().DB.QueryRowContext(ctx, "SELECT count(*) FROM grants WHERE principal_type='user' AND principal_id=?", user.Subject).Scan(&count); err != nil || count != 0 {
		t.Fatal(fmt.Sprint(count), err)
	}
}
