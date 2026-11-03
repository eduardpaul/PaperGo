package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"papergo/extensions/mcp"
)

// Caller is a resolved caller.
type Caller struct {
	subject string
	scopes  []string
	backend mcp.Backend
}

func (c *Caller) Subject() string            { return c.subject }
func (c *Caller) HasScope(scope string) bool { return slices.Contains(c.scopes, scope) }
func (c *Caller) Backend() mcp.Backend       { return c.backend }

// resolve asks the API who the request's caller is: GET /v1/identity/me
// reports the subject and the scopes its credential grants, its roles'
// grants narrowed to the credential's. Nothing is cached: every request
// asks again, and the API authorizes every request the tools make.
func resolve(ctx context.Context, api backend) (*Caller, error) {
	res, err := api.Do(ctx, mcp.Request{Method: http.MethodGet, Path: "/v1/identity/me"})
	if err != nil {
		return nil, err
	}
	var me struct {
		Subject string   `json:"subject"`
		Scopes  []string `json:"scopes"`
	}
	if err = json.Unmarshal(res.Body, &me); err != nil || me.Subject == "" {
		return nil, mcp.ErrUnavailable
	}
	return &Caller{subject: me.Subject, scopes: me.Scopes, backend: api}, nil
}
