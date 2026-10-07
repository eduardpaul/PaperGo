package config

import "testing"

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
