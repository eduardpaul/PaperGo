package config

import (
	"fmt"
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
}

func Load() (Config, error) {
	c := Config{Env: value("APP_ENV", "development"), Address: value("HTTP_ADDR", "127.0.0.1:8080"), DatabasePath: value("DATABASE_PATH", "data/papergo.db"), BlobPath: value("BLOB_PATH", "data/blobs"), AuthMode: value("AUTH_MODE", "oidc"), DevToken: os.Getenv("DEV_TOKEN"), DevSubject: value("DEV_SUBJECT", "local-admin"), Issuer: os.Getenv("OIDC_ISSUER"), Audience: os.Getenv("OIDC_AUDIENCE"), MaxUpload: 64 * 1024 * 1024, MaxInFlight: 32, RequestTimeout: 30 * time.Second, NodeID: value("NODE_ID", "local"), RunRetention: 30 * 24 * time.Hour}
	c.AdminSubject = os.Getenv("ADMIN_SUBJECT")
	c.GroupsClaim = os.Getenv("OIDC_GROUPS_CLAIM")
	c.GroupsPrefix = os.Getenv("OIDC_GROUPS_PREFIX")
	c.AdminGroup = os.Getenv("ADMIN_GROUP")
	c.TokenType = os.Getenv("OIDC_TOKEN_TYPE")
	c.GroupsFromUserInfo = os.Getenv("OIDC_GROUPS_FROM_USERINFO") == "true"
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
			return c, fmt.Errorf("production requires OIDC authentication")
		}
		if len(c.DevToken) < 32 {
			return c, fmt.Errorf("DEV_TOKEN requires at least 32 characters")
		}
		if strings.TrimSpace(c.DevSubject) == "" || len(c.DevSubject) > 255 {
			return c, fmt.Errorf("invalid DEV_SUBJECT")
		}
	case "oidc":
		if c.Issuer == "" || c.Audience == "" {
			return c, fmt.Errorf("OIDC_ISSUER and OIDC_AUDIENCE are required")
		}
		if c.Env == "production" && !strings.HasPrefix(c.Issuer, "https://") {
			return c, fmt.Errorf("production OIDC_ISSUER requires HTTPS")
		}
	default:
		return c, fmt.Errorf("AUTH_MODE must be development or oidc")
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
func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
