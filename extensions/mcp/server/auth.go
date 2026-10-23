package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"

	"papergo/extensions/mcp"
)

// CredentialResolver decides who a request acts for. Tools receive the
// resolved caller and never see the credential. The bearer relay is the
// only resolver; a later OAuth design supplies another without changing
// tools.
type CredentialResolver interface {
	Resolve(ctx context.Context, r *http.Request) (*Caller, error)
}

// Admission failures before MCP dispatch.
var (
	// errUnauthenticated: no usable credential, or the API refused it.
	errUnauthenticated = errors.New("unauthenticated")
	// errInvalidToken: the API refused the credential.
	errInvalidToken = errors.New("invalid token")
	// errForbidden: the API knows the caller but admits them nowhere, such
	// as an inactive user.
	errForbidden = errors.New("forbidden")
)

// Caller is a resolved caller.
type Caller struct {
	subject string
	scopes  []string
	backend mcp.Backend
	trace   *trace
}

func (c *Caller) Subject() string            { return c.subject }
func (c *Caller) HasScope(scope string) bool { return slices.Contains(c.scopes, scope) }
func (c *Caller) Backend() mcp.Backend       { return c.backend }

// trace collects the API request IDs a request caused, for its log.
type trace struct {
	mu  sync.Mutex
	ids []string
}

func (t *trace) add(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ids = append(t.ids, id)
}

func (t *trace) list() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.ids)
}

// bearer relays the request's bearer credential to the API: the caller is
// whoever GET /v1/identity/me says it is, with the scopes it reports, which
// already narrow the caller's grants to the credential's. Nothing is
// cached: every request asks again, and every API request the tools make
// authenticates again.
type bearer struct{ rest *rest }

func (b bearer) Resolve(ctx context.Context, r *http.Request) (*Caller, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return nil, errUnauthenticated
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsFunc(token, func(c rune) bool { return c <= ' ' || c > '~' }) {
		return nil, errUnauthenticated
	}
	t := &trace{}
	api := tracedBackend{backend{rest: b.rest, authorization: "Bearer " + token}, t}
	res, err := api.Do(ctx, mcp.Request{Method: http.MethodGet, Path: "/v1/identity/me"})
	var apiErr *mcp.APIError
	switch {
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized:
		return nil, errInvalidToken
	case errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden:
		return nil, errForbidden
	case err != nil:
		return nil, err
	}
	var me struct {
		Subject string   `json:"subject"`
		Active  *bool    `json:"active"`
		Scopes  []string `json:"scopes"`
	}
	if err = json.Unmarshal(res.Body, &me); err != nil || me.Subject == "" {
		return nil, mcp.ErrUnavailable
	}
	if me.Active != nil && !*me.Active {
		return nil, errForbidden
	}
	return &Caller{subject: me.Subject, scopes: me.Scopes, backend: api, trace: t}, nil
}

// tracedBackend records the API request IDs of a backend's requests.
type tracedBackend struct {
	backend
	trace *trace
}

func (b tracedBackend) Do(ctx context.Context, r mcp.Request) (*mcp.Response, error) {
	res, err := b.backend.Do(ctx, r)
	var id string
	var apiErr *mcp.APIError
	var failure *mcp.Failure
	switch {
	case res != nil:
		id = res.RequestID
	case errors.As(err, &apiErr):
		id = apiErr.RequestID
	case errors.As(err, &failure):
		id = failure.RequestID
	}
	if id != "" {
		b.trace.add(id)
	}
	return res, err
}
