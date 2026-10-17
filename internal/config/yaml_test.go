package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func documentedYAML(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestDocumentedConfigYAML(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, documentedYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "0.0.0.0:8080" {
		t.Fatalf("address %s", c.Address)
	}
	if c.DatabasePath != filepath.Join("data", "papergo.db") || c.SearchPath != filepath.Join("data", "search.db") || c.BlobPath != "./data/blobs" {
		t.Fatalf("paths database %s search %s blob %s", c.DatabasePath, c.SearchPath, c.BlobPath)
	}
	if c.PublicURL == nil || c.PublicURL.String() != "http://localhost:8080" {
		t.Fatalf("public url %#v", c.PublicURL)
	}
	if c.AuthMode != "local" || c.AdminSubject != "admin" || c.AdminPassword != "admin" || c.AdminPasswordOmitted {
		t.Fatalf("auth %+v", c)
	}
	if c.SearchStore != "zvec" || c.ZvecPath != "./data/search/zvec" {
		t.Fatalf("search %s %s", c.SearchStore, c.ZvecPath)
	}
	if c.MaxUpload != 100*1024*1024 {
		t.Fatalf("upload %d", c.MaxUpload)
	}
	t.Logf("documented YAML address %s database %s blob %s public %s mode %s user %s password %s store %s zvec %s upload %d", c.Address, c.DatabasePath, c.BlobPath, c.PublicURL, c.AuthMode, c.AdminSubject, c.AdminPassword, c.SearchStore, c.ZvecPath, c.MaxUpload)
}

func TestDefaultsAreLocalZvecOnLocalhost(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:8080" || c.AuthMode != "local" || c.AdminSubject != "admin" || c.AdminPassword != "admin" || c.SearchStore != "zvec" {
		t.Fatalf("defaults address %s mode %s user %s store %s", c.Address, c.AuthMode, c.AdminSubject, c.SearchStore)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if c.OCRModelsSet || c.OCRModels != filepath.Join(filepath.Dir(executable), "assets", "models", "paddleocr") {
		t.Fatalf("default OCR models set=%v path=%s", c.OCRModelsSet, c.OCRModels)
	}
	t.Logf("default address %s mode %s user %s password %s store %s ocr models %s set %v", c.Address, c.AuthMode, c.AdminSubject, c.AdminPassword, c.SearchStore, c.OCRModels, c.OCRModelsSet)
}

func TestEnvironmentOverridesYAML(t *testing.T) {
	c, err := LoadFrom(func(key string) string {
		if key == "HTTP_ADDR" {
			return "127.0.0.1:9090"
		}
		if key == "AUTH_MODE" {
			return "development"
		}
		if key == "APP_ENV" {
			return "test"
		}
		if key == "DEV_TOKEN" {
			return "0123456789abcdef0123456789abcdef"
		}
		if key == "SEARCH_STORE" {
			return "sqlite"
		}
		return ""
	}, documentedYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:9090" || c.AuthMode != "development" || c.SearchStore != "sqlite" || c.AdminPassword != "" {
		t.Fatalf("override address %s mode %s store %s password %q", c.Address, c.AuthMode, c.SearchStore, c.AdminPassword)
	}
	t.Logf("environment override address %s mode %s store %s", c.Address, c.AuthMode, c.SearchStore)
}

func TestExplicitOIDCAndProxyAndEmptyPassword(t *testing.T) {
	oidc := `
server:
  public_url: https://docs.example.com
auth:
  mode: oidc
  admin_group: admins
oidc:
  issuer: https://auth.example.com/application/o/papergo/
  audience: papergo
  client_id: papergo
  groups_claim: groups
`
	c, err := LoadFrom(func(key string) string {
		if key == "APP_ENV" {
			return "production"
		}
		return ""
	}, oidc)
	if err != nil || c.AuthMode != "oidc" || c.Issuer != "https://auth.example.com/application/o/papergo/" || c.Audience != "papergo" || c.ClientID != "papergo" || c.AdminGroup != "admins" {
		t.Fatalf("oidc %+v %v", c, err)
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err = os.WriteFile(secret, []byte(strings.Repeat("s", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	proxy := `
server:
  public_url: https://docs.example.com
auth:
  mode: proxy
  admin_user: papergo-admin
  proxy_secret_file: ` + secret + `
  proxy_trusted_cidrs: [172.29.242.2/32]
search:
  store: zvec
  zvec_path: ` + filepath.Join(dir, "zvec") + `
`
	c, err = LoadFrom(func(key string) string {
		if key == "APP_ENV" {
			return "production"
		}
		if key == "DATABASE_PATH" {
			return filepath.Join(dir, "papergo.db")
		}
		if key == "BLOB_PATH" {
			return filepath.Join(dir, "blobs")
		}
		return ""
	}, proxy)
	if err != nil || c.AuthMode != "proxy" || c.AdminSubject != "papergo-admin" || len(c.ProxyTrustedCIDRs) != 1 || c.SearchStore != "zvec" || c.ProxySecret != strings.Repeat("s", 32) {
		t.Fatalf("proxy %+v %v", c, err)
	}
	t.Logf("explicit oidc and proxy loaded; proxy store %s", c.SearchStore)
	empty := `
auth:
  mode: local
  admin_user: admin
  admin_password: ""
`
	c, err = LoadFrom(func(string) string { return "" }, empty)
	if err != nil || !c.AdminPasswordOmitted || c.AdminPassword != "" {
		t.Fatalf("empty password %+v %v", c, err)
	}
}

func TestNonInteractiveMissingPasswordFails(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err = PromptAdminPassword(reader, ioDiscard{}); err == nil {
		t.Fatal("non-interactive prompt invented a password")
	}
	t.Logf("non-interactive missing password: %v", err)
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestYAMLRejectsInvalidConfiguration(t *testing.T) {
	for name, text := range map[string]string{
		"unknown section":      "unknown: {}",
		"unknown field":        "auth:\n  admin_pasword: secret",
		"duplicate field":      "auth:\n  mode: local\n  mode: proxy",
		"malformed YAML":       "auth: [",
		"multiple documents":   "auth: {}\n---\nauth: {}",
		"scalar":               "plain text",
		"invalid port":         "server:\n  port: 0",
		"non-integer port":     "server:\n  port: http",
		"invalid bind":         "server:\n  bind: localhost",
		"empty data directory": "server:\n  data_dir: ''",
		"invalid upload limit": "storage:\n  max_upload_mb: 1025",
		"non-string password":  "auth:\n  admin_password: 1234",
		"non-list CIDRs":       "auth:\n  proxy_trusted_cidrs: 127.0.0.1/32",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadFrom(nil, text); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func TestYAMLPasswordPresenceAndEnvironmentOverride(t *testing.T) {
	for _, test := range []struct {
		name, text, password string
		omitted              bool
	}{
		{"absent", "auth: {}", "admin", false},
		{"empty", "auth:\n  admin_password: ''", "", true},
		{"null", "auth:\n  admin_password: null", "", true},
		{"blank", "auth:\n  admin_password:", "", true},
		{"quoted punctuation", "auth:\n  admin_password: ' p:# = true '", " p:# = true ", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, err := LoadFrom(nil, test.text)
			if err != nil || c.AdminPassword != test.password || c.AdminPasswordOmitted != test.omitted {
				t.Fatalf("password presence was not preserved: %+v, %v", c, err)
			}
			c, err = LoadFrom(func(key string) string {
				if key == "ADMIN_PASSWORD" {
					return "environment-password"
				}
				return ""
			}, test.text)
			if err != nil || c.AdminPassword != "environment-password" || c.AdminPasswordOmitted {
				t.Fatal("environment did not override YAML password", err)
			}
		})
	}
}

func TestYAMLConfigFileSelection(t *testing.T) {
	t.Chdir(t.TempDir())
	getenv := func(string) string { return "" }
	if text, err := readWorkingYAML(getenv); err != nil || text != "" {
		t.Fatal("absent default config", text, err)
	}
	const text = "auth:\n  admin_user: yaml-admin\n"
	if err := os.WriteFile("config.yaml", []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readWorkingYAML(getenv); err != nil || got != text {
		t.Fatal("default config", got, err)
	}
	explicit := func(string) string { return "server.yaml" }
	if _, err := readWorkingYAML(explicit); err == nil {
		t.Fatal("missing explicit configuration was ignored")
	}
	if err := os.WriteFile("server.yaml", []byte("auth: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readWorkingYAML(explicit); err != nil || got != "auth: {}" {
		t.Fatal("explicit config", got, err)
	}
}
