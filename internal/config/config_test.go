package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyConfigurationFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_MODE", "proxy")
	t.Setenv("ADMIN_SUBJECT", "papergo-admin")
	t.Setenv("PUBLIC_URL", "https://docs.example.com")
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(strings.Repeat("s", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PROXY_SECRET_FILE", secret)
	t.Setenv("PROXY_TRUSTED_CIDRS", "172.29.242.2/32")
	c, err := Load()
	if err != nil || len(c.ProxyTrustedCIDRs) != 1 {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{{"PROXY_TRUSTED_CIDRS", ""}, {"PROXY_TRUSTED_CIDRS", "0.0.0.0/0"}, {"PROXY_SECRET_FILE", "missing"}, {"PUBLIC_URL", "http://docs.example.com"}, {"ADMIN_SUBJECT", ""}, {"OIDC_GROUPS_CLAIM", "groups"}, {"ADMIN_GROUP", "admins"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid proxy configuration accepted")
			}
		})
	}
}

func TestProductionRejectsDevelopmentAuth(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted development auth")
	}
}
func TestConfigurationFailsClosed(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ISSUER", "")
	t.Setenv("OIDC_AUDIENCE", "")
	if _, err := Load(); err == nil {
		t.Fatal("missing OIDC settings accepted")
	}
}
func TestLoadLimitsValidatesAndDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	c, err := Load()
	if err != nil || c.MaxInFlight != 32 || c.RequestTimeout != 30*time.Second {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for key, bad := range map[string]string{"MAX_IN_FLIGHT": "0", "REQUEST_TIMEOUT": "10ms"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s accepted", key, bad)
			}
		})
	}
	t.Setenv("MAX_IN_FLIGHT", "8")
	t.Setenv("REQUEST_TIMEOUT", "5s")
	if c, err = Load(); err != nil || c.MaxInFlight != 8 || c.RequestTimeout != 5*time.Second {
		t.Fatalf("overrides: %+v %v", c, err)
	}
}

func TestOIDCRequiresExplicitFirstAdministrator(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_AUDIENCE", "papergo")
	t.Setenv("ADMIN_SUBJECT", "")
	if _, err := Load(); err == nil {
		t.Fatal("OIDC accepted missing administrator subject")
	}
	t.Setenv("ADMIN_SUBJECT", "configured-admin")
	if c, err := Load(); err != nil || c.AdminSubject != "configured-admin" {
		t.Fatal(c, err)
	}
}

func TestOIDCAdminGroupValidation(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_AUDIENCE", "papergo")

	// Both ADMIN_SUBJECT and ADMIN_GROUP set -> error
	t.Setenv("ADMIN_SUBJECT", "configured-admin")
	t.Setenv("ADMIN_GROUP", "papergo-admins")
	t.Setenv("OIDC_GROUPS_CLAIM", "groups")
	if _, err := Load(); err == nil {
		t.Fatal("accepted both ADMIN_SUBJECT and ADMIN_GROUP")
	}

	// ADMIN_GROUP without OIDC_GROUPS_CLAIM -> error
	t.Setenv("ADMIN_SUBJECT", "")
	t.Setenv("ADMIN_GROUP", "papergo-admins")
	t.Setenv("OIDC_GROUPS_CLAIM", "")
	if _, err := Load(); err == nil {
		t.Fatal("accepted ADMIN_GROUP without OIDC_GROUPS_CLAIM")
	}

	// Valid ADMIN_GROUP with OIDC_GROUPS_CLAIM -> success
	t.Setenv("OIDC_GROUPS_CLAIM", "groups")
	t.Setenv("ADMIN_GROUP", "papergo-admins")
	c, err := Load()
	if err != nil || c.AdminGroup != "papergo-admins" || c.AdminSubject != "" {
		t.Fatalf("valid ADMIN_GROUP rejected: %v, config: %+v", err, c)
	}
}

func TestPublicURLIsAnOrigin(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	c, err := Load()
	if err != nil || c.PublicURL != nil {
		t.Fatalf("default: %+v %v", c.PublicURL, err)
	}
	for _, bad := range []string{"docs.example.com", "ftp://docs.example.com", "https://", "https://docs.example.com/papergo", "https://docs.example.com/?a=1", "https://user@docs.example.com", "https://docs.example.com/#x"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("PUBLIC_URL", bad)
			if _, err := Load(); err == nil {
				t.Fatalf("PUBLIC_URL=%s accepted", bad)
			}
		})
	}
	t.Setenv("PUBLIC_URL", "https://docs.example.com:8443/")
	if c, err = Load(); err != nil || c.PublicURL.String() != "https://docs.example.com:8443" {
		t.Fatalf("origin: %v %v", c.PublicURL, err)
	}
}
func TestProductionPublicURLRequiresHTTPS(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_AUDIENCE", "papergo")
	t.Setenv("ADMIN_SUBJECT", "configured-admin")
	t.Setenv("PUBLIC_URL", "http://docs.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("production accepted an http PUBLIC_URL")
	}
	t.Setenv("PUBLIC_URL", "https://docs.example.com")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}
