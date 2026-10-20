// Package server serves PaperGo's MCP tools over stateless Streamable HTTP
// at /v1/mcp. Each request is authenticated with its own bearer credential
// against the configured PaperGo API, and served by a server holding only
// the tools that caller may use. It owns no data: every tool acts through
// the API as the caller.
package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"
)

// Path is where the MCP endpoint is served.
const Path = "/v1/mcp"

// Config configures the server.
type Config struct {
	// BackendURL is the PaperGo API's origin.
	BackendURL *url.URL
	// PublicURL is the endpoint's absolute public URL behind an ingress;
	// its origin is accepted from browsers.
	PublicURL *url.URL
	// AllowedOrigins are further origins accepted from browsers.
	AllowedOrigins []string
	// Version is reported to clients.
	Version string
	Logger  *slog.Logger

	// Operating limits; zero values take the defaults.
	MaxInFlight     int
	AdmissionWait   time.Duration
	RequestTimeout  time.Duration
	MaxRequestBytes int64
	MaxBackendBytes int64
	MaxBackendConns int
}

// Default operating limits.
const (
	DefaultMaxInFlight     = 32
	DefaultAdmissionWait   = time.Second
	DefaultRequestTimeout  = 30 * time.Second
	DefaultMaxRequestBytes = 1 << 20
	DefaultMaxBackendBytes = 8 << 20
	DefaultMaxBackendConns = 64
)

func (c *Config) defaults() {
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = DefaultMaxInFlight
	}
	if c.AdmissionWait <= 0 {
		c.AdmissionWait = DefaultAdmissionWait
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = DefaultRequestTimeout
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = DefaultMaxRequestBytes
	}
	if c.MaxBackendBytes <= 0 {
		c.MaxBackendBytes = DefaultMaxBackendBytes
	}
	if c.MaxBackendConns <= 0 {
		c.MaxBackendConns = DefaultMaxBackendConns
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// ParseBackendURL checks the API's origin: an http or https URL without
// credentials, path, query or fragment. Plain http is accepted only for a
// loopback or private address or a single-label host, such as a service on
// a private container network.
func ParseBackendURL(raw string) (*url.URL, error) {
	u, err := parseOrigin(raw)
	if err != nil {
		return nil, fmt.Errorf("backend URL: %w", err)
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("backend URL: give the API's origin, without a path")
	}
	u.Path = ""
	if u.Scheme == "http" && !private(u.Hostname()) {
		return nil, errors.New("backend URL: use https, or http only to a loopback, private or single-label host")
	}
	return u, nil
}

// ParsePublicURL checks the endpoint's public URL: absolute, ending in
// /v1/mcp, https unless on loopback.
func ParsePublicURL(raw string) (*url.URL, error) {
	u, err := parseOrigin(raw)
	if err != nil {
		return nil, fmt.Errorf("public URL: %w", err)
	}
	if u.Path != Path {
		return nil, fmt.Errorf("public URL: the path must be %s", Path)
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return nil, errors.New("public URL: use https, or http only on loopback")
	}
	return u, nil
}

// ParseOrigins checks a comma-separated list of browser origins.
func ParseOrigins(raw string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		u, err := parseOrigin(part)
		if err != nil || u.Path != "" {
			return nil, fmt.Errorf("allowed origin %q must be scheme://host[:port]", part)
		}
		out = append(out, origin(u))
	}
	return out, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	switch {
	case err != nil:
		return nil, err
	case u.Scheme != "http" && u.Scheme != "https" || u.Host == "" || u.Opaque != "":
		return nil, errors.New("must be an absolute http or https URL")
	case u.User != nil:
		return nil, errors.New("must not carry credentials")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
		return nil, errors.New("must not have a query or fragment")
	}
	return u, nil
}

func origin(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func private(host string) bool {
	if loopback(host) || !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsPrivate()
}
