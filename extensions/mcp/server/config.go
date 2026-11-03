// Package server serves PaperGo's MCP tools over stateless Streamable HTTP
// at /v1/mcp, as a module of the PaperGo API. Each request acts for the
// API caller whose bearer credential it carries, and is served by a server
// holding only the tools that caller may use. It owns no data: every tool
// acts through the API in-process, as the caller, so the API authorizes
// each of its requests exactly as it would the same REST request.
package server

import (
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Path is where the MCP endpoint is served.
const Path = "/v1/mcp"

// Config configures the server.
type Config struct {
	// Version is reported to clients.
	Version string
	// AppURL is the web app's public origin (PUBLIC_URL) results link to;
	// nil links to the origin each client reached.
	AppURL *url.URL
	Logger *slog.Logger
	// wrapAPI, when set, wraps the API handler tools act through, so this
	// package's tests can watch or fail its answers.
	wrapAPI func(http.Handler) http.Handler

	// Operating limits; zero values take the defaults.
	RequestTimeout  time.Duration
	MaxRequestBytes int64
	MaxResultBytes  int64
}

// Default operating limits.
const (
	DefaultRequestTimeout  = 30 * time.Second
	DefaultMaxRequestBytes = 1 << 20
	DefaultMaxResultBytes  = 8 << 20
)

func (c *Config) defaults() {
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if c.MaxResultBytes <= 0 {
		c.MaxResultBytes = DefaultMaxResultBytes
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}
