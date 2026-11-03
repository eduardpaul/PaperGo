package server

import (
	"net/http"
	"net/url"
	"slices"

	"papergo/extensions/mcp"
)

// Caller is a resolved caller.
type Caller struct {
	subject string
	scopes  []string
	backend mcp.Backend
	app     *url.URL
}

func (c *Caller) Subject() string            { return c.subject }
func (c *Caller) HasScope(scope string) bool { return slices.Contains(c.scopes, scope) }
func (c *Caller) Backend() mcp.Backend       { return c.backend }
func (c *Caller) AppURL() *url.URL           { return c.app }

// appURL is the web app's origin for a request: the configured public
// origin, or the one the client reached, as the API's own links use.
func appURL(public *url.URL, r *http.Request) *url.URL {
	if public != nil {
		return public
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return &url.URL{Scheme: scheme, Host: r.Host}
}
