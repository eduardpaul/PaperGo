package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"papergo/internal/identity"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string
	Address      string
	DatabasePath string
	BlobPath     string
	AuthMode     string
	DevToken     string
	DevSubject   string
	AdminSubject string
	Issuer       string
	Audience     string
	MaxUpload    int64
	// MaxInFlight bounds concurrent API requests; excess requests are shed with 503.
	MaxInFlight int
	// RequestTimeout cancels API reads that outlive it; mutations and blob transfers are exempt.
	RequestTimeout time.Duration
	// NodeID identifies this process to the workflow runner; a restarted
	// node recovers the runs it was executing.
	NodeID string
	// RunRetention is how long finished workflow runs are kept.
	RunRetention time.Duration
	// PublicURL is the scheme and host clients use, for example behind a
	// TLS-terminating reverse proxy. Nil uses each request's own.
	PublicURL *url.URL
	// GroupsClaim is the claim holding group names (e.g. "groups" or "realm_access.roles").
	GroupsClaim string
	// GroupsPrefix filters group names to those with this prefix, stripped before syncing.
	GroupsPrefix string
	// AdminGroup names an IdP group assigned the Administrator role.
	AdminGroup string
	// TokenType, when set, enforces the JOSE header typ (e.g. "at+jwt").
	TokenType string
	// GroupsFromUserInfo enables fetching groups from UserInfo when missing in access token.
	GroupsFromUserInfo bool
	// ClientID is the public OIDC client the web frontend signs in with
	// (authorization code with PKCE). Empty disables browser sign-in.
	ClientID string
	// ClientScopes are the scopes the web frontend requests from the issuer.
	ClientScopes      string
	ProxySecret       string
	ProxyTrustedCIDRs []netip.Prefix
}

func Load() (Config, error) {
	c := Config{Env: value("APP_ENV", "development"), Address: value("HTTP_ADDR", "127.0.0.1:8080"), DatabasePath: value("DATABASE_PATH", "data/papergo.db"), BlobPath: value("BLOB_PATH", "data/blobs"), AuthMode: value("AUTH_MODE", "oidc"), DevToken: os.Getenv("DEV_TOKEN"), DevSubject: value("DEV_SUBJECT", "local-admin"), Issuer: os.Getenv("OIDC_ISSUER"), Audience: os.Getenv("OIDC_AUDIENCE"), MaxUpload: 64 * 1024 * 1024, MaxInFlight: 32, RequestTimeout: 30 * time.Second, NodeID: value("NODE_ID", "local"), RunRetention: 30 * 24 * time.Hour}
	c.AdminSubject = os.Getenv("ADMIN_SUBJECT")
	c.GroupsClaim = os.Getenv("OIDC_GROUPS_CLAIM")
	c.GroupsPrefix = os.Getenv("OIDC_GROUPS_PREFIX")
	c.AdminGroup = os.Getenv("ADMIN_GROUP")
	c.TokenType = os.Getenv("OIDC_TOKEN_TYPE")
	c.GroupsFromUserInfo = os.Getenv("OIDC_GROUPS_FROM_USERINFO") == "true"
	c.ClientID = os.Getenv("OIDC_CLIENT_ID")
	c.ClientScopes = os.Getenv("OIDC_CLIENT_SCOPES")
	if v := os.Getenv("MAX_UPLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 1024*1024*1024 {
			return c, fmt.Errorf("MAX_UPLOAD_BYTES must be between 1 and 1073741824")
		}
		c.MaxUpload = n
	}
	if v := os.Getenv("MAX_IN_FLIGHT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			return c, fmt.Errorf("MAX_IN_FLIGHT must be between 1 and 10000")
		}
		c.MaxInFlight = n
	}
	if v := os.Getenv("REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second || d > 10*time.Minute {
			return c, fmt.Errorf("REQUEST_TIMEOUT must be a duration between 1s and 10m")
		}
		c.RequestTimeout = d
	}
	if v := os.Getenv("RUN_RETENTION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour || d > 10*365*24*time.Hour {
			return c, fmt.Errorf("RUN_RETENTION must be a duration between 1h and 87600h")
		}
		c.RunRetention = d
	}
	if v := os.Getenv("PUBLIC_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("PUBLIC_URL must be an http or https origin without a path, such as https://docs.example.com")
		}
		c.PublicURL = &url.URL{Scheme: u.Scheme, Host: u.Host}
	}
	if strings.TrimSpace(c.NodeID) == "" || len(c.NodeID) > 100 {
		return c, fmt.Errorf("NODE_ID needs 1 to 100 characters")
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("invalid APP_ENV")
	}
	if c.Env == "production" && c.PublicURL != nil && c.PublicURL.Scheme != "https" {
		return c, fmt.Errorf("production PUBLIC_URL requires HTTPS")
	}
	switch c.AuthMode {
	case "development":
		if c.AdminSubject == "" && c.AdminGroup == "" {
			c.AdminSubject = c.DevSubject
		}
		if c.Env == "production" {
			return c, fmt.Errorf("production requires oidc or proxy authentication")
		}
		if len(c.DevToken) < 32 {
			return c, fmt.Errorf("DEV_TOKEN requires at least 32 characters")
		}
		if strings.TrimSpace(c.DevSubject) == "" || len(c.DevSubject) > 255 {
			return c, fmt.Errorf("invalid DEV_SUBJECT")
		}
		if c.ClientID != "" || c.ClientScopes != "" {
			return c, fmt.Errorf("OIDC_CLIENT_ID and OIDC_CLIENT_SCOPES require AUTH_MODE=oidc")
		}
	case "oidc":
		if c.Issuer == "" || c.Audience == "" {
			return c, fmt.Errorf("OIDC_ISSUER and OIDC_AUDIENCE are required")
		}
		if c.Env == "production" && !strings.HasPrefix(c.Issuer, "https://") {
			return c, fmt.Errorf("production OIDC_ISSUER requires HTTPS")
		}
		if err := c.validateBrowserClient(); err != nil {
			return c, err
		}
	case "proxy":
		if c.AdminSubject == "" || c.AdminGroup != "" {
			return c, fmt.Errorf("proxy authentication requires ADMIN_SUBJECT and does not support ADMIN_GROUP")
		}
		if c.PublicURL == nil || c.PublicURL.Scheme != "https" {
			return c, fmt.Errorf("proxy authentication requires an HTTPS PUBLIC_URL")
		}
		secretPath := os.Getenv("PROXY_SECRET_FILE")
		if secretPath == "" {
			return c, fmt.Errorf("PROXY_SECRET_FILE is required")
		}
		secret, err := os.ReadFile(secretPath)
		if err != nil {
			return c, fmt.Errorf("read proxy secret file: %w", err)
		}
		c.ProxySecret = strings.TrimSpace(string(secret))
		if len(c.ProxySecret) < 32 || len(c.ProxySecret) > 256 || strings.ContainsAny(c.ProxySecret, "\r\n\x00") {
			return c, fmt.Errorf("proxy secret must contain 32 to 256 bytes without line breaks")
		}
		for _, raw := range strings.Split(os.Getenv("PROXY_TRUSTED_CIDRS"), ",") {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil {
				return c, fmt.Errorf("PROXY_TRUSTED_CIDRS requires valid CIDRs")
			}
			if prefix.Bits() == 0 {
				return c, fmt.Errorf("proxy trust cannot cover every address")
			}
			c.ProxyTrustedCIDRs = append(c.ProxyTrustedCIDRs, prefix.Masked())
		}
		if c.GroupsClaim != "" || c.GroupsPrefix != "" || c.GroupsFromUserInfo || c.TokenType != "" || c.Issuer != "" || c.Audience != "" || c.ClientID != "" || c.ClientScopes != "" {
			return c, fmt.Errorf("proxy authentication does not accept OIDC settings")
		}
	default:
		return c, fmt.Errorf("AUTH_MODE must be development, oidc or proxy")
	}
	if c.AdminGroup != "" {
		if c.GroupsClaim == "" {
			return c, fmt.Errorf("ADMIN_GROUP requires OIDC_GROUPS_CLAIM")
		}
		if err := identity.ValidateName(c.AdminGroup, ""); err != nil {
			return c, fmt.Errorf("invalid ADMIN_GROUP: %w", err)
		}
	}
	if c.AdminSubject != "" && c.AdminGroup != "" {
		return c, fmt.Errorf("exactly one of ADMIN_SUBJECT and ADMIN_GROUP must be set")
	}
	if c.AdminSubject == "" && c.AdminGroup == "" {
		return c, fmt.Errorf("exactly one of ADMIN_SUBJECT and ADMIN_GROUP is required")
	}
	if c.AdminSubject != "" {
		if c.AdminSubject != strings.TrimSpace(c.AdminSubject) || len(c.AdminSubject) > 255 {
			return c, fmt.Errorf("ADMIN_SUBJECT must identify the first organization administrator")
		}
	}
	return c, nil
}

// DefaultClientScopes request an ID token, profile and group claims, and a
// refresh token so the browser renews access without another redirect.
const DefaultClientScopes = "openid profile email offline_access"

func (c *Config) validateBrowserClient() error {
	if c.ClientID == "" {
		if c.ClientScopes != "" {
			return fmt.Errorf("OIDC_CLIENT_SCOPES requires OIDC_CLIENT_ID")
		}
		return nil
	}
	if len(c.ClientID) > 255 || strings.IndexFunc(c.ClientID, func(r rune) bool { return r <= ' ' || r == 0x7f }) >= 0 {
		return fmt.Errorf("OIDC_CLIENT_ID must be a client identifier without spaces")
	}
	if c.ClientScopes == "" {
		c.ClientScopes = DefaultClientScopes
	}
	scopes := strings.Fields(c.ClientScopes)
	openid := false
	for _, scope := range scopes {
		// RFC 6749 section 3.3 scope-token characters.
		if strings.IndexFunc(scope, func(r rune) bool { return r < 0x21 || r > 0x7e || r == '"' || r == '\\' }) >= 0 {
			return fmt.Errorf("OIDC_CLIENT_SCOPES contains an invalid scope")
		}
		openid = openid || scope == "openid"
	}
	if !openid {
		return fmt.Errorf("OIDC_CLIENT_SCOPES must include openid")
	}
	if len(c.ClientScopes) > 1024 {
		return fmt.Errorf("OIDC_CLIENT_SCOPES must not exceed 1024 bytes")
	}
	c.ClientScopes = strings.Join(scopes, " ")
	return nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
