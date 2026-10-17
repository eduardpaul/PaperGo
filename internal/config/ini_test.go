package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const documentedINI = `
[server]
port = 8080
bind = 0.0.0.0
data_dir = ./data
public_url = http://localhost:8080

[auth]
mode = local
admin_user = admin
admin_password = admin

[search]
store = zvec
zvec_path = ./data/search/zvec

[storage]
blob_path = ./data/blobs
max_upload_mb = 100
`

func TestDocumentedConfigINI(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, documentedINI)
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
	t.Logf("documented ini address %s database %s blob %s public %s mode %s user %s password %s store %s zvec %s upload %d", c.Address, c.DatabasePath, c.BlobPath, c.PublicURL, c.AuthMode, c.AdminSubject, c.AdminPassword, c.SearchStore, c.ZvecPath, c.MaxUpload)
}

func TestDefaultsAreLocalZvecOnLocalhost(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:8080" || c.AuthMode != "local" || c.AdminSubject != "admin" || c.AdminPassword != "admin" || c.SearchStore != "zvec" {
		t.Fatalf("defaults address %s mode %s user %s store %s", c.Address, c.AuthMode, c.AdminSubject, c.SearchStore)
	}
	if c.OCRModelsSet || c.OCRModels != "models/paddleocr" {
		t.Fatalf("default OCR models set=%v path=%s", c.OCRModelsSet, c.OCRModels)
	}
	t.Logf("default address %s mode %s user %s password %s store %s ocr models %s set %v", c.Address, c.AuthMode, c.AdminSubject, c.AdminPassword, c.SearchStore, c.OCRModels, c.OCRModelsSet)
}

func TestEnvironmentOverridesINI(t *testing.T) {
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
	}, documentedINI)
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
[server]
public_url = https://docs.example.com
[auth]
mode = oidc
admin_group = admins
[oidc]
issuer = https://auth.example.com/application/o/papergo/
audience = papergo
client_id = papergo
groups_claim = groups
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
[server]
public_url = https://docs.example.com
[auth]
mode = proxy
admin_user = papergo-admin
proxy_secret_file = ` + secret + `
proxy_trusted_cidrs = 172.29.242.2/32
[search]
store = zvec
zvec_path = ` + filepath.Join(dir, "zvec") + `
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
[auth]
mode = local
admin_user = admin
admin_password =
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
