package config

import (
	"testing"
	"time"
)

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
