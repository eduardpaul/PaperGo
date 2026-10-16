package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchDatabasePathRejectsApplicationDatabase(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	dir := t.TempDir()
	path := filepath.Join(dir, "search.db")
	t.Setenv("DATABASE_PATH", path)
	t.Setenv("SEARCH_DATABASE_PATH", "")
	if _, err := Load(); err == nil {
		t.Fatal("default search path overlaps the application database")
	}
	t.Setenv("SEARCH_DATABASE_PATH", filepath.Join(dir, "index.db"))
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("application database"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"same", "symlink", "hardlink", "parent symlink", "wal"} {
		t.Run(kind, func(t *testing.T) {
			index := path
			switch kind {
			case "symlink":
				index = filepath.Join(dir, "symbolic.db")
				if err := os.Symlink(path, index); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				index = filepath.Join(dir, "hard.db")
				if err := os.Link(path, index); err != nil {
					t.Fatal(err)
				}
			case "parent symlink":
				parent := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(dir, parent); err != nil {
					t.Fatal(err)
				}
				index = filepath.Join(parent, "search.db")
			case "wal":
				index = path + "-wal"
			}
			t.Setenv("SEARCH_DATABASE_PATH", index)
			if _, err := Load(); err == nil {
				t.Fatalf("overlap accepted: %s", index)
			}
		})
	}
}

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
	for _, tc := range []struct{ key, value string }{{"PROXY_TRUSTED_CIDRS", ""}, {"PROXY_TRUSTED_CIDRS", "0.0.0.0/0"}, {"PROXY_SECRET_FILE", "missing"}, {"PUBLIC_URL", "http://docs.example.com"}, {"ADMIN_SUBJECT", ""}, {"OIDC_GROUPS_CLAIM", "groups"}, {"ADMIN_GROUP", "admins"}, {"OIDC_CLIENT_ID", "papergo"}} {
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
	if err != nil || c.MaxInFlight != 32 || c.RequestTimeout != 30*time.Second || strings.Join(c.SearchLanguages, ",") != "english" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	for key, bad := range map[string]string{"MAX_IN_FLIGHT": "0", "REQUEST_TIMEOUT": "10ms", "SEARCH_LANGUAGES": "english,klingon"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%s accepted", key, bad)
			}
		})
	}
	t.Setenv("MAX_IN_FLIGHT", "8")
	t.Setenv("REQUEST_TIMEOUT", "5s")
	t.Setenv("SEARCH_LANGUAGES", " German, english ")
	if c, err = Load(); err != nil || c.MaxInFlight != 8 || c.RequestTimeout != 5*time.Second || strings.Join(c.SearchLanguages, ",") != "german,english" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	t.Setenv("SEARCH_LANGUAGES", "none")
	if c, err = Load(); err != nil || len(c.SearchLanguages) != 0 {
		t.Fatalf("no stemming: %+v %v", c.SearchLanguages, err)
	}
	if c.Embeddings.Enabled() {
		t.Fatal("embeddings are on by default")
	}
}

func TestEmbeddingsConfig(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte(" sk-test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_EMBEDDINGS_PROVIDER", "openai")
	t.Setenv("AI_EMBEDDINGS_ENDPOINT", "http://localhost:11434/v1")
	t.Setenv("AI_EMBEDDINGS_MODEL", "nomic-embed-text")
	t.Setenv("AI_EMBEDDINGS_API_KEY_FILE", key)
	t.Setenv("AI_EMBEDDINGS_DIMENSIONS", "256")
	c, err := Load()
	if err != nil || !c.Embeddings.Enabled() || c.Embeddings.APIKey != "sk-test" || c.Embeddings.Dimensions != 256 || c.Embeddings.Model != "nomic-embed-text" {
		t.Fatalf("%+v %v", c.Embeddings, err)
	}
	t.Run("local", func(t *testing.T) {
		t.Setenv("AI_EMBEDDINGS_PROVIDER", "local")
		t.Setenv("AI_EMBEDDINGS_MODEL", "")
		t.Setenv("ONNXRUNTIME_LIB", "/opt/ort/libonnxruntime.so")
		t.Setenv("AI_EMBEDDINGS_THREADS", "4")
		c, err := Load()
		if err != nil || c.Embeddings.Model != "embeddinggemma-2" || c.Embeddings.Models != filepath.Join("models", "embedding", "embeddinggemma-2") || c.Embeddings.Threads != 4 || c.Embeddings.Dimensions != 256 {
			t.Fatalf("%+v %v", c.Embeddings, err)
		}
		t.Setenv("AI_EMBEDDINGS_DIMENSIONS", "300")
		if _, err := Load(); err == nil {
			t.Fatal("Gemma with 300 dimensions")
		}
	})
	for key, bad := range map[string]string{"AI_EMBEDDINGS_PROVIDER": "azure", "AI_EMBEDDINGS_MODEL": "", "AI_EMBEDDINGS_DIMENSIONS": "0", "AI_EMBEDDINGS_ENDPOINT": "localhost:11434", "AI_EMBEDDINGS_API_KEY_FILE": "missing"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q accepted", key, bad)
			}
		})
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

func TestOIDCBrowserClient(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "oidc")
	t.Setenv("OIDC_ISSUER", "https://issuer.example")
	t.Setenv("OIDC_AUDIENCE", "papergo")
	t.Setenv("ADMIN_SUBJECT", "configured-admin")
	if c, err := Load(); err != nil || c.ClientID != "" || c.ClientScopes != "" {
		t.Fatal("browser sign-in is optional", c, err)
	}
	t.Setenv("OIDC_CLIENT_ID", "papergo")
	if c, err := Load(); err != nil || c.ClientID != "papergo" || c.ClientScopes != DefaultClientScopes {
		t.Fatal("default browser scopes", c, err)
	}
	t.Setenv("OIDC_CLIENT_SCOPES", "  openid   profile groups ")
	if c, err := Load(); err != nil || c.ClientScopes != "openid profile groups" {
		t.Fatal("normalized browser scopes", c, err)
	}
	for _, tc := range []struct{ key, value string }{{"OIDC_CLIENT_SCOPES", "profile email"}, {"OIDC_CLIENT_SCOPES", `openid "quoted"`}, {"OIDC_CLIENT_SCOPES", "openid " + strings.Repeat("s", 1024)}, {"OIDC_CLIENT_ID", "two words"}, {"OIDC_CLIENT_ID", strings.Repeat("c", 256)}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid browser client accepted")
			}
		})
	}
	t.Setenv("OIDC_CLIENT_ID", "")
	if _, err := Load(); err == nil {
		t.Fatal("scopes without a client accepted")
	}
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("OIDC_CLIENT_SCOPES", "")
	t.Setenv("OIDC_CLIENT_ID", "papergo")
	if _, err := Load(); err == nil {
		t.Fatal("development authentication accepted a browser OIDC client")
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

func TestChatConfig(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	c, err := Load()
	if err != nil || c.Chat.Enabled() {
		t.Fatalf("chat is on by default: %+v %v", c.Chat, err)
	}
	t.Setenv("AI_CHAT_PROVIDER", "openai")
	t.Setenv("AI_CHAT_ENDPOINT", "http://localhost:11434/v1")
	t.Setenv("AI_CHAT_MODEL", "llama3.2")
	if c, err = Load(); err != nil || !c.Chat.Enabled() || c.Chat.Model != "llama3.2" || c.Chat.APIKey != "" {
		t.Fatalf("%+v %v", c.Chat, err)
	}
	for key, bad := range map[string]string{"AI_CHAT_PROVIDER": "local", "AI_CHAT_MODEL": "", "AI_CHAT_ENDPOINT": "localhost", "AI_CHAT_API_KEY_FILE": "missing"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q accepted", key, bad)
			}
		})
	}
}

func TestZvecPathDoesNotOverlapData(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{string(os.PathSeparator), root, filepath.Join(root, "papergo.db"), filepath.Join(root, "papergo.db-wal"), filepath.Join(root, "blobs"), filepath.Join(root, "blobs", "index")} {
		if err := distinctZvecPath(path, filepath.Join(root, "papergo.db"), filepath.Join(root, "blobs"), filepath.Join(root, "search.db")); err == nil {
			t.Errorf("accepted overlapping directory %s", path)
		}
	}
	if err := distinctZvecPath(filepath.Join(root, "search", "zvec"), filepath.Join(root, "papergo.db"), filepath.Join(root, "blobs"), filepath.Join(root, "search.db")); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionConfiguration(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("AUTH_MODE", "development")
	t.Setenv("DEV_TOKEN", "0123456789abcdef0123456789abcdef")
	t.Setenv("AI_DECISION_MODELS_PATH", "")
	t.Setenv("ONNXRUNTIME_LIB", "")
	c, err := Load()
	if err != nil || c.Decisions.Models != "" {
		t.Fatal("decisions must be optional", c.Decisions, err)
	}
	t.Setenv("AI_DECISION_MODELS_PATH", "models/decision")
	if _, err = Load(); err == nil {
		t.Fatal("enabled decisions without native library")
	}
	t.Setenv("ONNXRUNTIME_LIB", "/lib/libonnxruntime.so")
	c, err = Load()
	if err != nil || c.Decisions.Models != "models/decision" || c.Decisions.Library != c.OCRLibrary {
		t.Fatal(c.Decisions, err)
	}
	for key, value := range map[string]string{"AI_DECISION_THREADS": "33", "AI_DECISION_MAX_TOKENS": "1025", "AI_DECISION_HEAD_TOKENS": "257"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := Load(); err == nil {
				t.Fatal("accepted unsupported setting", key, value)
			}
		})
	}
}
