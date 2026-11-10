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
	if c.SearchStore != "sqlite" {
		t.Fatalf("search %s", c.SearchStore)
	}
	if c.MaxUpload != 100*1024*1024 {
		t.Fatalf("upload %d", c.MaxUpload)
	}
	if !c.MCP {
		t.Fatal("MCP is off by default")
	}
	t.Logf("documented YAML address %s database %s blob %s public %s mode %s user %s password %s store %s upload %d", c.Address, c.DatabasePath, c.BlobPath, c.PublicURL, c.AuthMode, c.AdminSubject, c.AdminPassword, c.SearchStore, c.MaxUpload)
}

func TestDefaultsAreLocalSQLiteOnLocalhost(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:8080" || c.AuthMode != "local" || c.AdminSubject != "admin" || c.AdminPassword != "admin" || c.SearchStore != "sqlite" {
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
			return "none"
		}
		return ""
	}, documentedYAML(t))
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "127.0.0.1:9090" || c.AuthMode != "development" || c.SearchStore != "none" || c.AdminPassword != "" {
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
  store: none
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
	if err != nil || c.AuthMode != "proxy" || c.AdminSubject != "papergo-admin" || len(c.ProxyTrustedCIDRs) != 1 || c.SearchStore != "none" || c.ProxySecret != strings.Repeat("s", 32) {
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

func TestWorkersYAML(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, `workers:
  documents:
    concurrency: 2
    window:
      time_zone: Europe/Madrid
      from: "22:00"
      to: "06:00"
  maintenance:
    admission:
      policy: threshold_or_max_wait
      threshold: 50
      max_wait: 2h
      batch: 100
      flush:
        cron: "0 3 * * *"
        time_zone: UTC
        missed: once
`)
	if err != nil {
		t.Fatal(err)
	}
	d, m := c.Workers["documents"], c.Workers["maintenance"]
	if d.Concurrency == nil || *d.Concurrency != 2 || d.Window == nil || d.Window.From != "22:00" || d.Admission != nil {
		t.Fatalf("documents %+v", d)
	}
	if a := m.Admission; a == nil || a.Threshold != 50 || a.MaxWait != "2h" || a.Batch != 100 || a.Flush == nil || a.Flush.Missed != "once" {
		t.Fatalf("maintenance %+v", m)
	}
	if _, err = LoadFrom(func(string) string { return "" }, "workers:\n  documents:\n    concurency: 2\n"); err == nil {
		t.Fatal("an unknown worker setting was accepted")
	}
}

func TestMCPYAML(t *testing.T) {
	c, err := LoadFrom(func(string) string { return "" }, "mcp:\n  enabled: false\n  extensions: [acme.crm]\n")
	if err != nil || c.MCP || strings.Join(c.MCPExtensions, ",") != "acme.crm" {
		t.Fatalf("mcp %v %v %v", c.MCP, c.MCPExtensions, err)
	}
	c, err = LoadFrom(func(key string) string {
		if key == "MCP" {
			return "on"
		}
		return ""
	}, "mcp:\n  enabled: false\n")
	if err != nil || !c.MCP {
		t.Fatalf("environment over YAML %v %v", c.MCP, err)
	}
}

// The listen address follows the same sources as the rest: the admin
// health probe reads it with Load.
func TestListenAddress(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PAPERGO_CONFIG", "")
	t.Setenv("HTTP_ADDR", "")
	address := func() string {
		t.Helper()
		c, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		return c.Address
	}
	if got := address(); got != "127.0.0.1:8080" {
		t.Fatalf("default %q", got)
	}
	path := filepath.Join(dir, "papergo.yaml")
	if err := os.WriteFile(path, []byte("server:\n  bind: 0.0.0.0\n  port: 9090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAPERGO_CONFIG", path)
	if got := address(); got != "0.0.0.0:9090" {
		t.Fatalf("config.yaml %q", got)
	}
	t.Setenv("HTTP_ADDR", "127.0.0.1:7070")
	if got := address(); got != "127.0.0.1:7070" {
		t.Fatalf("environment %q", got)
	}
}

// Every setting the loader reads has a config.yaml key, so a deployment
// can keep its whole configuration in one file.
func TestEverySettingHasAYAMLKey(t *testing.T) {
	covered := map[string]bool{}
	for _, s := range (&yamlConfig{}).settings() {
		if covered[s.env] {
			t.Errorf("%s has two YAML keys", s.env)
		}
		covered[s.env] = true
	}
	for _, key := range derivedKeys {
		covered[key] = true
	}
	dir := t.TempDir()
	read := map[string]bool{}
	// Each environment takes the loader down other branches; failures past
	// the keys read do not matter here.
	for _, env := range []map[string]string{
		{},
		{"AUTH_MODE": "development", "APP_ENV": "test"},
		{"AUTH_MODE": "oidc"},
		{"AUTH_MODE": "proxy", "PUBLIC_URL": "https://docs.example.com", "ADMIN_SUBJECT": "admin"},
		{"AUTH_MODE": "local", "ADMIN_PASSWORD_FILE": filepath.Join(dir, "missing")},
		{"AI_EMBEDDINGS_PROVIDER": "openai", "AI_EMBEDDINGS_API_KEY_FILE": filepath.Join(dir, "missing")},
		{"AI_EMBEDDINGS_PROVIDER": "local", "ONNXRUNTIME_LIB": filepath.Join(dir, "lib.so")},
		{"AI_DECISION_MODELS_PATH": dir},
		{"AI_CHAT_PROVIDER": "openai", "AI_CHAT_MODEL": "m", "AI_CHAT_API_KEY_FILE": filepath.Join(dir, "missing")},
		{"AI_BATCH_PROVIDER": "openai", "AI_BATCH_MODEL": "m", "AI_BATCH_MAX_TOKENS": "1000", "AI_BATCH_API_KEY_FILE": filepath.Join(dir, "missing")},
		{"ORGANIZATION_LOCALE": "es-ES"},
	} {
		_, _ = LoadFrom(func(key string) string {
			read[key] = true
			return env[key]
		}, "")
	}
	for key := range read {
		if !covered[key] {
			t.Errorf("%s has no config.yaml key", key)
		}
	}
	if len(read) < 60 {
		t.Fatalf("only %d settings were read; the loader changed shape", len(read))
	}
}

// Installation defaults are below config.yaml, which is below the
// environment; a later layer keeps what it leaves out.
func TestYAMLLayers(t *testing.T) {
	defaults := "server:\n  bind: 0.0.0.0\n  port: 8080\n  data_dir: /data\n  environment: production\nauth:\n  admin_password: from-defaults\n"
	config := "server:\n  port: 9090\nstorage:\n  blob_path: /mnt/blobs\nsearch:\n  languages: [spanish, english]\n"
	c, err := loadLayers(func(string) string { return "" }, t.TempDir(), defaults, config)
	if err != nil {
		t.Fatal(err)
	}
	if c.Address != "0.0.0.0:9090" || c.Env != "production" || c.AdminPassword != "from-defaults" {
		t.Fatalf("address %s env %s password %q", c.Address, c.Env, c.AdminPassword)
	}
	if c.DatabasePath != filepath.Join("/data", "papergo.db") || c.SearchPath != filepath.Join("/data", "search.db") || c.BlobPath != "/mnt/blobs" {
		t.Fatalf("paths %s %s %s", c.DatabasePath, c.SearchPath, c.BlobPath)
	}
	if strings.Join(c.SearchLanguages, ",") != "spanish,english" {
		t.Fatalf("search languages %v", c.SearchLanguages)
	}
	c, err = loadLayers(func(key string) string {
		return map[string]string{"HTTP_ADDR": "127.0.0.1:7070", "BLOB_PATH": "/env/blobs"}[key]
	}, t.TempDir(), defaults, config)
	if err != nil || c.Address != "127.0.0.1:7070" || c.BlobPath != "/env/blobs" || c.DatabasePath != filepath.Join("/data", "papergo.db") {
		t.Fatalf("environment over layers: %s %s %s %v", c.Address, c.BlobPath, c.DatabasePath, err)
	}
}

// Load and the admin commands read DefaultsFile beside the executable
// before config.yaml.
func TestReadLayers(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	getenv := func(string) string { return "" }
	if layers, err := readLayers(getenv, dir); err != nil || len(layers) != 1 || layers[0] != "" {
		t.Fatal("without files", layers, err)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultsFile), []byte("server:\n  data_dir: /data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("config.yaml", []byte("server:\n  port: 9090\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	layers, err := readLayers(getenv, dir)
	if err != nil || len(layers) != 2 || !strings.Contains(layers[0], "/data") || !strings.Contains(layers[1], "9090") {
		t.Fatal("layers", layers, err)
	}
}

func TestAISettingsInYAML(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "key")
	if err := os.WriteFile(key, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(func(string) string { return "" }, `ai:
  execution: batch
  chat:
    provider: openai
    endpoint: http://ollama:11434/v1
    model: gemma4
  batch:
    provider: anthropic
    model: claude-haiku-5-5
    api_key_file: `+key+`
    max_tokens: 4096
    deadline: 24h
  embeddings:
    provider: openai
    endpoint: http://ollama:11434/v1
    model: embeddinggemma
    dimensions: 768
retention:
  runs: 240h
quotas:
  per_creator: 10
`)
	if err != nil {
		t.Fatal(err)
	}
	if c.AIExecution != "batch" || c.Chat.Model != "gemma4" || c.Chat.Endpoint != "http://ollama:11434/v1" {
		t.Fatalf("chat %+v execution %s", c.Chat, c.AIExecution)
	}
	if c.Batch.Provider != "anthropic" || c.Batch.APIKey != "secret" || c.Batch.MaxTokens != 4096 || c.BatchDeadline.Hours() != 24 {
		t.Fatalf("batch %+v deadline %s", c.Batch, c.BatchDeadline)
	}
	if c.Embeddings.Provider != "openai" || c.Embeddings.Dimensions != 768 || c.RunRetention.Hours() != 240 || c.OperationQuotas.PerCreator != 10 {
		t.Fatalf("embeddings %+v retention %s quotas %+v", c.Embeddings, c.RunRetention, c.OperationQuotas)
	}
	if _, err = LoadFrom(nil, "ai:\n  batch:\n    deadline: 30s\n"); err == nil {
		t.Fatal("YAML skipped the validation its environment variable gets")
	}
}

// docs/configuration.md lists every setting.
func TestConfigurationDocsListEverySetting(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	keys := append([]string{}, derivedKeys...)
	for _, s := range (&yamlConfig{}).settings() {
		keys = append(keys, s.env)
	}
	for _, key := range keys {
		if !strings.Contains(string(raw), "`"+key+"`") {
			t.Errorf("docs/configuration.md does not list %s", key)
		}
	}
	for _, locale := range LocalePresets() {
		if !strings.Contains(string(raw), "`"+locale+"`") {
			t.Errorf("docs/configuration.md does not list the %s preset", locale)
		}
	}
}

// The admin commands find the data where the server keeps it.
func TestLoadReadsDataPathsFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "papergo.yaml")
	if err := os.WriteFile(path, []byte("server:\n  data_dir: /srv/papergo\nstorage:\n  blob_path: /mnt/blobs\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PAPERGO_CONFIG", path)
	for _, key := range []string{"DATABASE_PATH", "BLOB_PATH", "SEARCH_DATABASE_PATH", "DATABASE_URL"} {
		t.Setenv(key, "")
	}
	c, err := Load()
	if err != nil || c.DatabasePath != filepath.Join("/srv/papergo", "papergo.db") || c.BlobPath != "/mnt/blobs" || c.SearchPath != filepath.Join("/srv/papergo", "search.db") || !c.SearchPathSet {
		t.Fatalf("%s %s %s %v %v", c.DatabasePath, c.BlobPath, c.SearchPath, c.SearchPathSet, err)
	}
	t.Setenv("DATABASE_PATH", filepath.Join(dir, "env.db"))
	if c, err = Load(); err != nil || c.DatabasePath != filepath.Join(dir, "env.db") {
		t.Fatalf("environment %s %v", c.DatabasePath, err)
	}
}
