package httpapi

import (
	"net/http"
	"papergo/internal/identity"
)

func (a *API) registerIdentity(m *http.ServeMux) {
	a.registerPreferences(m)
	a.registerInboxes(m)
	d := a.DMS.Directory()
	m.HandleFunc("GET /v1/identity/me/tokens", foundationRead(a, func(r *http.Request) (any, error) { return d.RESTTokens(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/identity/me/tokens", foundationMutation(a, 201, false, func(r *http.Request, in identity.RESTTokenInput, _ int) (any, error) {
		return d.CreateRESTToken(r.Context(), subject(r), in)
	}))
	m.HandleFunc("DELETE /v1/identity/me/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := d.RevokeRESTToken(r.Context(), subject(r), r.PathValue("id")); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("GET /v1/organization", foundationRead(a, func(r *http.Request) (any, error) { return d.Organization(r.Context(), subject(r)) }))
	m.HandleFunc("PATCH /v1/organization", foundationMutation(a, 200, true, func(r *http.Request, in identity.OrganizationUpdate, v int) (any, error) {
		return d.UpdateOrganization(r.Context(), subject(r), v, in)
	}))
	m.HandleFunc("GET /v1/people", foundationRead(a, func(r *http.Request) (any, error) {
		return a.DMS.People(r.Context(), subject(r))
	}))
	m.HandleFunc("GET /v1/identity/users/{subject}/status", foundationRead(a, func(r *http.Request) (any, error) {
		return d.UserStatus(r.Context(), subject(r), r.PathValue("subject"))
	}))
	m.HandleFunc("PUT /v1/identity/users/{subject}/status", foundationMutation(a, 200, false, func(r *http.Request, in struct {
		Active *bool `json:"active"`
	}, _ int) (any, error) {
		if in.Active == nil {
			return nil, &identity.ValidationError{Message: "active is required"}
		}
		return d.SetUserActive(r.Context(), subject(r), r.PathValue("subject"), *in.Active)
	}))
	m.HandleFunc("GET /v1/identity/scopes", foundationRead(a, func(r *http.Request) (any, error) {
		if err := a.DMS.RequireScope(r.Context(), subject(r), "role.read"); err != nil {
			return nil, err
		}
		return identity.Catalog, nil
	}))
	m.HandleFunc("GET /v1/identity/me/scopes", foundationRead(a, func(r *http.Request) (any, error) { return identity.EffectiveScopes(r.Context(), d.DB, subject(r)) }))
	m.HandleFunc("GET /v1/identity/users", foundationRead(a, func(r *http.Request) (any, error) { return d.Users(r.Context(), subject(r)) }))
	m.HandleFunc("GET /v1/identity/me", foundationRead(a, func(r *http.Request) (any, error) { return d.Me(r.Context(), subject(r)) }))
	m.HandleFunc("PATCH /v1/identity/me", foundationMutation(a, 200, false, func(r *http.Request, in struct {
		DisplayName *string `json:"display_name"`
	}, _ int) (any, error) {
		return d.UpdateMe(r.Context(), subject(r), in.DisplayName)
	}))
	m.HandleFunc("GET /v1/identity/users/{subject}", foundationRead(a, func(r *http.Request) (any, error) { return d.User(r.Context(), subject(r), r.PathValue("subject")) }))
	m.HandleFunc("PATCH /v1/identity/users/{subject}", foundationMutation(a, 200, false, func(r *http.Request, in identity.UserUpdate, _ int) (any, error) {
		return d.UpdateUser(r.Context(), subject(r), r.PathValue("subject"), in)
	}))
	m.HandleFunc("DELETE /v1/identity/users/{subject}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.DMS.DeleteUser(r.Context(), subject(r), r.PathValue("subject")); err != nil {
			a.failure(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
	m.HandleFunc("POST /v1/identity/users", foundationMutation(a, 201, false, func(r *http.Request, in identity.UserInput, _ int) (any, error) {
		return d.CreateUser(r.Context(), subject(r), in)
	}))
	m.HandleFunc("GET /v1/identity/groups", foundationRead(a, func(r *http.Request) (any, error) { return d.Groups(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/identity/groups", foundationMutation(a, 201, false, func(r *http.Request, in identity.GroupInput, _ int) (any, error) {
		return d.SaveGroup(r.Context(), subject(r), "", 0, in)
	}))
	m.HandleFunc("PUT /v1/identity/groups/{id}", foundationMutation(a, 200, true, func(r *http.Request, in identity.GroupInput, v int) (any, error) {
		return d.SaveGroup(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/identity/groups/{id}", a.deletion(func(r *http.Request, v int) error {
		return d.DeleteGroup(r.Context(), subject(r), r.PathValue("id"), v)
	}))
	m.HandleFunc("GET /v1/identity/groups/{id}/members", foundationRead(a, func(r *http.Request) (any, error) { return d.Members(r.Context(), subject(r), r.PathValue("id")) }))
	for _, method := range []string{"PUT", "DELETE"} {
		m.HandleFunc(method+" /v1/identity/groups/{id}/members/{principalType}/{principalID}", func(w http.ResponseWriter, r *http.Request) {
			err := d.SetMember(r.Context(), subject(r), r.PathValue("id"), identity.Principal{Type: r.PathValue("principalType"), ID: r.PathValue("principalID")}, r.Method == "DELETE")
			if err != nil {
				a.failure(w, r, err)
				return
			}
			w.WriteHeader(204)
		})
	}
	m.HandleFunc("GET /v1/identity/roles", foundationRead(a, func(r *http.Request) (any, error) { return d.Roles(r.Context(), subject(r)) }))
	m.HandleFunc("POST /v1/identity/roles", foundationMutation(a, 201, false, func(r *http.Request, in identity.RoleInput, _ int) (any, error) {
		return d.SaveRole(r.Context(), subject(r), "", 0, in)
	}))
	m.HandleFunc("PUT /v1/identity/roles/{id}", foundationMutation(a, 200, true, func(r *http.Request, in identity.RoleInput, v int) (any, error) {
		return d.SaveRole(r.Context(), subject(r), r.PathValue("id"), v, in)
	}))
	m.HandleFunc("DELETE /v1/identity/roles/{id}", a.deletion(func(r *http.Request, v int) error { return d.DeleteRole(r.Context(), subject(r), r.PathValue("id"), v) }))
	m.HandleFunc("GET /v1/identity/roles/{id}/assignments", foundationRead(a, func(r *http.Request) (any, error) { return d.Assignments(r.Context(), subject(r), r.PathValue("id")) }))
	for _, method := range []string{"PUT", "DELETE"} {
		m.HandleFunc(method+" /v1/identity/roles/{id}/assignments/{principalType}/{principalID}", func(w http.ResponseWriter, r *http.Request) {
			err := d.SetAssignment(r.Context(), subject(r), r.PathValue("id"), identity.Principal{Type: r.PathValue("principalType"), ID: r.PathValue("principalID")}, r.Method == "DELETE")
			if err != nil {
				a.failure(w, r, err)
				return
			}
			w.WriteHeader(204)
		})
	}
}
