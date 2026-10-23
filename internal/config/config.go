package config

import (
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"papergo/internal/ai"
	"papergo/internal/decision"
	"papergo/internal/identity"
	"papergo/internal/search"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string
	Address      string
	DatabasePath string
	// DatabaseURL is a PostgreSQL connection URL (DATABASE_URL). When set,
	// PaperGo and the workflow runner keep their tables in that database
	// instead of the SQLite file at DatabasePath, which then only anchors
	// the default locations of local files beside it.
	DatabaseURL string
	BlobPath    string
	// SearchPath is the search store's SQLite file (SEARCH_DATABASE_PATH),
	// by default search.db next to the database.
	SearchPath string
	// SearchStore is the search extension's store (SEARCH_STORE): zvec
	// (default), sqlite, or none to run without search.
	SearchStore           string
	ZvecPath              string
	ZvecKeywordCandidates int
	// SearchLanguages are the languages whose word stems search indexes
	// (SEARCH_LANGUAGES, comma-separated Snowball names; default english,
	// none for no stemming).
	SearchLanguages []string
	AuthMode        string
	DevToken        string
	DevSubject      string
	AdminSubject    string
	Issuer          string
	Audience        string
	MaxUpload       int64
	// MaxInFlight bounds concurrent API requests; excess requests are shed with 503.
	MaxInFlight int
	// RequestTimeout cancels API reads that outlive it; mutations and blob transfers are exempt.
	RequestTimeout time.Duration
	// RunRetention is how long finished workflow runs, dispatched events
	// and failed runs are kept.
	RunRetention time.Duration
	// SystemRunRetention is how long successful automatic runs of system
	// built-ins are kept. RunRetention is a ceiling.
	SystemRunRetention time.Duration
	// ResultCacheTTL is the idle lifetime of reusable activity answers.
	// It is independent of RunRetention.
	ResultCacheTTL time.Duration
	// OperationQuotas bound the pending operations people may queue
	// (OPERATION_QUOTA_*); zero leaves a bound off.
	OperationQuotas OperationQuotas
	// Workers adjust the operation runtime's named workers (config.yaml
	// workers); the executable validates them against its workers.
	Workers map[string]Worker
	// PublicURL is the scheme and host clients use, for example behind a
	// TLS-terminating reverse proxy. Nil uses each request's own.
	PublicURL *url.URL
	// MCPPublicURL is where AI assistants reach the optional MCP server,
	// papergo-mcp, ending in /v1/mcp; the web app offers assistant
	// connections only when it is set.
	MCPPublicURL *url.URL
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
	ClientScopes string
	// OCRLibrary is the ONNX Runtime shared library OCR loads; empty turns
	// OCR, and the workflow activities that need it, off.
	OCRLibrary string
	// OCRModels is the directory of the PaddleOCR model files.
	OCRModels string
	// OCRModelsSet is true when OCR_MODELS was configured. The default
	// directory may be absent; an explicit directory must exist.
	OCRModelsSet bool
	// OCRThreads is the CPU threads one OCR inference uses.
	OCRThreads int
	// Embeddings is the embedding model of semantic search
	// (AI_EMBEDDINGS_*); off by default.
	Embeddings ai.EmbeddingsConfig
	// Decisions is the optional local Laya engine.
	Decisions decision.Config
	// Chat is the chat model of AI workflow steps (AI_CHAT_*); off by
	// default.
	Chat ai.ChatConfig
	// Batch is the batch endpoint (AI_BATCH_*). It is independent of Chat
	// and off by default.
	Batch ai.BatchConfig
	// AIExecution is the default for remote LLM actions: immediate or batch.
	// An action's execution input overrides it. Empty is treated as immediate.
	AIExecution string
	// BatchDeadline is how long a parked batch request waits.
	BatchDeadline     time.Duration
	ProxySecret       string
	ProxyTrustedCIDRs []netip.Prefix
	// AdminPassword is the local administrator's initial password. It is
	// applied only when that account has no password yet.
	AdminPassword string
	// AdminPasswordOmitted marks an explicitly cleared password or a missing
	// production password. A non-interactive first start then requires an
	// existing administrator instead of inventing a password.
	AdminPasswordOmitted bool
}

// Search stores (SEARCH_STORE).
const (
	SearchZvec   = "zvec"
	SearchSQLite = "sqlite"
	// SearchNone runs PaperGo without its search extension: listings match
	// search text by name.
	SearchNone = "none"
)

// Load reads config.yaml from PAPERGO_CONFIG or ./config.yaml, then applies
// environment variables. Environment variables win when they are non-empty.
func Load() (Config, error) {
	text, err := readWorkingYAML(os.Getenv)
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(os.Getenv, text)
}

// LoadFrom builds configuration from YAML text and getenv. Tests pass the
// documented file and a map-backed getenv; Load uses the process environment.
func LoadFrom(getenv func(string) string, yamlText string) (Config, error) {
	executable, err := os.Executable()
	if err != nil {
		return Config{}, fmt.Errorf("find executable directory: %w", err)
	}
	return loadFrom(getenv, yamlText, filepath.Dir(executable))
}

func loadFrom(getenv func(string) string, yamlText, directory string) (Config, error) {
	file, err := parseYAML(yamlText)
	if err != nil {
		return Config{}, err
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	lookup := func(key string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return file.values[key]
	}
	value := func(key, fallback string) string {
		if v := lookup(key); v != "" {
			return v
		}
		return fallback
	}
	c := Config{Env: value("APP_ENV", "development"), Address: value("HTTP_ADDR", "127.0.0.1:8080"), DatabasePath: value("DATABASE_PATH", "data/papergo.db"), BlobPath: value("BLOB_PATH", "data/blobs"), AuthMode: value("AUTH_MODE", "local"), DevToken: lookup("DEV_TOKEN"), DevSubject: value("DEV_SUBJECT", "local-admin"), Issuer: lookup("OIDC_ISSUER"), Audience: lookup("OIDC_AUDIENCE"), MaxUpload: 64 * 1024 * 1024, MaxInFlight: 32, RequestTimeout: 30 * time.Second, RunRetention: 30 * 24 * time.Hour, SystemRunRetention: 24 * time.Hour, ResultCacheTTL: 30 * 24 * time.Hour}
	c.DatabaseURL = lookup("DATABASE_URL")
	if c.DatabaseURL != "" {
		if u, err := url.Parse(c.DatabaseURL); err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" {
			return c, fmt.Errorf("DATABASE_URL must be a postgres:// URL")
		}
	}
	c.SearchPath = value("SEARCH_DATABASE_PATH", filepath.Join(filepath.Dir(c.DatabasePath), "search.db"))
	c.SearchStore = value("SEARCH_STORE", SearchZvec)
	if c.SearchStore != SearchSQLite && c.SearchStore != SearchZvec && c.SearchStore != SearchNone {
		return c, fmt.Errorf("SEARCH_STORE must be zvec, sqlite or none")
	}
	c.ZvecPath = value("SEARCH_ZVEC_PATH", filepath.Join(filepath.Dir(c.DatabasePath), "search", "zvec"))
	c.ZvecKeywordCandidates = 10000
	if raw := lookup("SEARCH_ZVEC_KEYWORD_CANDIDATES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 99999 {
			return c, fmt.Errorf("SEARCH_ZVEC_KEYWORD_CANDIDATES must be between 1 and 99999")
		}
		c.ZvecKeywordCandidates = n
	}
	if c.SearchStore == SearchZvec {
		if err := distinctZvecPath(c.ZvecPath, c.DatabasePath, c.BlobPath, c.SearchPath); err != nil {
			return c, err
		}
	}
	if c.SearchStore == SearchSQLite {
		if err := distinctSearchPath(c.DatabasePath, c.SearchPath); err != nil {
			return c, err
		}
	}
	if languages := strings.ToLower(strings.TrimSpace(value("SEARCH_LANGUAGES", "english"))); languages != "none" {
		for _, l := range strings.Split(languages, ",") {
			if l = strings.TrimSpace(l); l != "" {
				c.SearchLanguages = append(c.SearchLanguages, l)
			}
		}
	}
	if _, err := search.NewStemmer(c.SearchLanguages); err != nil {
		return c, fmt.Errorf("SEARCH_LANGUAGES: %w", err)
	}
	c.AdminSubject = lookup("ADMIN_SUBJECT")
	c.GroupsClaim = lookup("OIDC_GROUPS_CLAIM")
	c.GroupsPrefix = lookup("OIDC_GROUPS_PREFIX")
	c.AdminGroup = lookup("ADMIN_GROUP")
	c.TokenType = lookup("OIDC_TOKEN_TYPE")
	c.GroupsFromUserInfo = lookup("OIDC_GROUPS_FROM_USERINFO") == "true"
	c.ClientID = lookup("OIDC_CLIENT_ID")
	c.ClientScopes = lookup("OIDC_CLIENT_SCOPES")
	if v := lookup("MAX_UPLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 1024*1024*1024 {
			return c, fmt.Errorf("MAX_UPLOAD_BYTES must be between 1 and 1073741824")
		}
		c.MaxUpload = n
	}
	if v := lookup("MAX_IN_FLIGHT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			return c, fmt.Errorf("MAX_IN_FLIGHT must be between 1 and 10000")
		}
		c.MaxInFlight = n
	}
	if v := lookup("REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second || d > 10*time.Minute {
			return c, fmt.Errorf("REQUEST_TIMEOUT must be a duration between 1s and 10m")
		}
		c.RequestTimeout = d
	}
	for _, setting := range []struct {
		key string
		dst *time.Duration
	}{
		{"RUN_RETENTION", &c.RunRetention},
		{"SYSTEM_RUN_RETENTION", &c.SystemRunRetention},
		{"RESULT_CACHE_TTL", &c.ResultCacheTTL},
	} {
		v := lookup(setting.key)
		if v == "" {
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour || d > 10*365*24*time.Hour {
			return c, fmt.Errorf("%s must be a duration between 1h and 87600h", setting.key)
		}
		*setting.dst = d
	}
	c.Workers = file.workers
	c.OperationQuotas = OperationQuotas{PerCreator: 1000, PerWorkspace: 5000, Installation: 50000, WorkspacePayloadBytes: 256 << 20}
	for _, setting := range []struct {
		key string
		dst *int64
	}{
		{"OPERATION_QUOTA_PER_CREATOR", &c.OperationQuotas.PerCreator},
		{"OPERATION_QUOTA_PER_WORKSPACE", &c.OperationQuotas.PerWorkspace},
		{"OPERATION_QUOTA_INSTALLATION", &c.OperationQuotas.Installation},
		{"OPERATION_QUOTA_WORKSPACE_PAYLOAD_BYTES", &c.OperationQuotas.WorkspacePayloadBytes},
	} {
		v := lookup(setting.key)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return c, fmt.Errorf("%s must be zero (no bound) or a positive count", setting.key)
		}
		*setting.dst = n
	}
	c.OCRLibrary, c.OCRThreads = lookup("ONNXRUNTIME_LIB"), 1
	if c.OCRLibrary == "" {
		library := filepath.Join(directory, "lib", "libonnxruntime.so")
		switch runtime.GOOS {
		case "windows":
			library = filepath.Join(directory, "onnxruntime.dll")
		case "darwin":
			library = filepath.Join(directory, "lib", "libonnxruntime.dylib")
		}
		if info, err := os.Stat(library); err == nil && info.Mode().IsRegular() {
			c.OCRLibrary = library
		}
	}
	models := filepath.Join(directory, "assets", "models")
	if configured := lookup("OCR_MODELS"); configured != "" {
		c.OCRModels, c.OCRModelsSet = configured, true
	} else {
		c.OCRModels = filepath.Join(models, "paddleocr")
	}
	if err := c.loadEmbeddings(models); err != nil {
		return c, err
	}
	if err := c.loadDecisions(models); err != nil {
		return c, err
	}
	if err := c.loadChat(); err != nil {
		return c, err
	}
	if err := c.loadBatch(); err != nil {
		return c, err
	}
	if v := lookup("OCR_THREADS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 32 {
			return c, fmt.Errorf("OCR_THREADS must be between 1 and 32")
		}
		c.OCRThreads = n
	}
	if v := lookup("PUBLIC_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("PUBLIC_URL must be an http or https origin without a path, such as https://docs.example.com")
		}
		c.PublicURL = &url.URL{Scheme: u.Scheme, Host: u.Host}
	}
	if v := lookup("MCP_PUBLIC_URL"); v != "" {
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" || u.Path != "/v1/mcp" || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("MCP_PUBLIC_URL must be the MCP server's absolute http or https URL ending in /v1/mcp, such as https://docs.example.com/v1/mcp")
		}
		c.MCPPublicURL = u
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("invalid APP_ENV")
	}
	switch c.AuthMode {
	case "local":
		if c.AdminGroup != "" {
			return c, fmt.Errorf("local authentication does not support ADMIN_GROUP")
		}
		if c.AdminSubject == "" {
			c.AdminSubject = "admin"
		}
		passwordFile := lookup("ADMIN_PASSWORD_FILE")
		if passwordFile != "" && lookup("ADMIN_PASSWORD") != "" {
			return c, fmt.Errorf("set only one of ADMIN_PASSWORD and ADMIN_PASSWORD_FILE")
		}
		switch {
		case passwordFile != "":
			file, err := os.Open(passwordFile)
			if err != nil {
				return c, fmt.Errorf("read ADMIN_PASSWORD_FILE: %w", err)
			}
			password, err := io.ReadAll(io.LimitReader(file, 75))
			file.Close()
			if err != nil {
				return c, fmt.Errorf("read ADMIN_PASSWORD_FILE: %w", err)
			}
			c.AdminPassword = strings.TrimRight(string(password), "\r\n")
			if len(password) == 75 || len(c.AdminPassword) == 0 || len(c.AdminPassword) > 72 {
				return c, fmt.Errorf("ADMIN_PASSWORD_FILE must contain a password of 1 to 72 bytes")
			}
		case lookup("ADMIN_PASSWORD") != "":
			c.AdminPassword = lookup("ADMIN_PASSWORD")
		case file.passwordSpecified:
			c.AdminPassword = file.password
			c.AdminPasswordOmitted = file.password == ""
		case c.Env == "production":
			c.AdminPasswordOmitted = true
		default:
			c.AdminPassword = "admin"
		}
		if c.ClientID != "" || c.ClientScopes != "" || c.Issuer != "" || c.Audience != "" {
			return c, fmt.Errorf("OIDC settings require AUTH_MODE=oidc")
		}
	case "development":
		if c.AdminSubject == "" && c.AdminGroup == "" {
			c.AdminSubject = c.DevSubject
		}
		if c.Env == "production" {
			return c, fmt.Errorf("production requires local, oidc or proxy authentication")
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
		secretPath := lookup("PROXY_SECRET_FILE")
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
		for _, raw := range strings.Split(lookup("PROXY_TRUSTED_CIDRS"), ",") {
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
		return c, fmt.Errorf("AUTH_MODE must be local, development, oidc or proxy")
	}
	if c.Env == "production" && c.PublicURL != nil && c.PublicURL.Scheme != "https" && c.AuthMode != "local" {
		return c, fmt.Errorf("production PUBLIC_URL requires HTTPS")
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

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(abs))
		abs = filepath.Dir(abs)
	}
}

// distinctSearchPath resolves existing symlinks, including parent directories
// of files not created yet, and detects hard links to the application database.
func distinctSearchPath(databasePath, searchPath string) error {

	db, err := canonicalPath(databasePath)
	if err != nil {
		return err
	}
	index, err := canonicalPath(searchPath)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".lease"} {
		path := db + suffix
		if path == index {
			return fmt.Errorf("SEARCH_DATABASE_PATH must not overlap DATABASE_PATH or its sidecar files")
		}
		left, lerr := os.Stat(path)
		right, rerr := os.Stat(index)
		if lerr == nil && rerr == nil && os.SameFile(left, right) {
			return fmt.Errorf("SEARCH_DATABASE_PATH must not overlap DATABASE_PATH or its sidecar files")
		}
	}
	return nil
}

// Native stores are disposable directories; never allow them to contain, or
// sit inside, another data store. Resolve aliases before comparing parents.
func distinctZvecPath(indexPath string, otherPaths ...string) error {
	index, err := canonicalPath(indexPath)
	if err != nil {
		return err
	}
	for _, p := range otherPaths {
		other, err := canonicalPath(p)
		if err != nil {
			return err
		}
		contains := func(parent, child string) bool {
			relative, err := filepath.Rel(parent, child)
			return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
		}
		if contains(index, other) || contains(other, index) {
			return fmt.Errorf("SEARCH_ZVEC_PATH must not overlap DATABASE_PATH, BLOB_PATH or SEARCH_DATABASE_PATH")
		}
		if err = distinctSearchPath(p, index); err != nil {
			return err
		}
	}
	return nil
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

// loadEmbeddings reads AI_EMBEDDINGS_*: the provider (none, local or
// openai), the model and optional dimensions; for local, the model directory,
// threads and token limit, with ONNX Runtime from ONNXRUNTIME_LIB and a
// statically linked tokenizer; for openai, an endpoint and an API key
// file.
func (c *Config) loadEmbeddings(models string) error {
	e := ai.EmbeddingsConfig{Provider: strings.ToLower(value("AI_EMBEDDINGS_PROVIDER", ai.ProviderNone)), Model: os.Getenv("AI_EMBEDDINGS_MODEL")}
	numbers := []struct {
		key      string
		into     *int
		min, max int
	}{{"AI_EMBEDDINGS_DIMENSIONS", &e.Dimensions, 1, ai.MaxDimensions}, {"AI_EMBEDDINGS_THREADS", &e.Threads, 1, 32}, {"AI_EMBEDDINGS_MAX_TOKENS", &e.MaxTokens, 2, 32768}}
	switch e.Provider {
	case ai.ProviderNone:
		c.Embeddings = e
		return nil
	case ai.ProviderLocal:
		e.Model = value("AI_EMBEDDINGS_MODEL", "embeddinggemma-2")
		e.Models = value("AI_EMBEDDINGS_MODELS_PATH", filepath.Join(models, "embedding", e.Model))
		e.Library = c.OCRLibrary
		if e.Library == "" {
			return fmt.Errorf("AI_EMBEDDINGS_PROVIDER=local requires ONNXRUNTIME_LIB")
		}
	case ai.ProviderOpenAI:
		e.Endpoint = value("AI_EMBEDDINGS_ENDPOINT", ai.DefaultEndpoint)
		numbers = numbers[:1]
		if path := os.Getenv("AI_EMBEDDINGS_API_KEY_FILE"); path != "" {
			key, err := readAPIKey(path)
			if err != nil {
				return fmt.Errorf("AI_EMBEDDINGS_API_KEY_FILE: %w", err)
			}
			e.APIKey = key
		}
	default:
		return fmt.Errorf("AI_EMBEDDINGS_PROVIDER must be none, local or openai")
	}
	for _, n := range numbers {
		if v := os.Getenv(n.key); v != "" {
			parsed, err := strconv.Atoi(v)
			if err != nil || parsed < n.min || parsed > n.max {
				return fmt.Errorf("%s must be between %d and %d", n.key, n.min, n.max)
			}
			*n.into = parsed
		}
	}
	if err := e.Validate(); err != nil {
		return fmt.Errorf("AI_EMBEDDINGS: %w", err)
	}
	c.Embeddings = e
	return nil
}

// loadChat reads AI_CHAT_*: the provider (none or openai), an
// OpenAI-compatible endpoint, the model and an API key file.
func (c *Config) loadChat() error {
	chat := ai.ChatConfig{Provider: strings.ToLower(value("AI_CHAT_PROVIDER", ai.ProviderNone)), Endpoint: value("AI_CHAT_ENDPOINT", ai.DefaultEndpoint), Model: os.Getenv("AI_CHAT_MODEL")}
	if path := os.Getenv("AI_CHAT_API_KEY_FILE"); path != "" && chat.Enabled() {
		key, err := readAPIKey(path)
		if err != nil {
			return fmt.Errorf("AI_CHAT_API_KEY_FILE: %w", err)
		}
		chat.APIKey = key
	}
	if err := chat.Validate(); err != nil {
		return fmt.Errorf("AI_CHAT: %w", err)
	}
	c.Chat = chat
	return nil
}

// OperationQuotas bound pending operations per creator, per workspace and
// for the installation, and the payload bytes of a workspace.
type OperationQuotas struct {
	PerCreator, PerWorkspace, Installation, WorkspacePayloadBytes int64
}

// loadBatch reads AI_BATCH_*: an independent provider, endpoint, model and
// credential file. AI_EXECUTION is the default for remote LLM actions.
// AI_BATCH_DEADLINE bounds a parked request.
func (c *Config) loadBatch() error {
	batch := ai.BatchConfig{
		Provider: strings.ToLower(value("AI_BATCH_PROVIDER", ai.ProviderNone)),
		Endpoint: os.Getenv("AI_BATCH_ENDPOINT"),
		Model:    os.Getenv("AI_BATCH_MODEL"),
	}
	if strings.TrimSpace(batch.Endpoint) == "" {
		if c.Chat.Enabled() && c.Chat.Endpoint != "" {
			batch.Endpoint = c.Chat.Endpoint
		} else {
			batch.Endpoint = ai.DefaultEndpoint
		}
	}
	if strings.TrimSpace(batch.Model) == "" {
		batch.Model = c.Chat.Model
	}
	if path := os.Getenv("AI_BATCH_API_KEY_FILE"); path != "" && batch.Enabled() {
		key, err := readAPIKey(path)
		if err != nil {
			return fmt.Errorf("AI_BATCH_API_KEY_FILE: %w", err)
		}
		batch.APIKey = key
	}
	if err := batch.Validate(); err != nil {
		return fmt.Errorf("AI_BATCH: %w", err)
	}
	c.Batch = batch
	c.AIExecution = strings.ToLower(value("AI_EXECUTION", "immediate"))
	if c.AIExecution != "immediate" && c.AIExecution != "batch" {
		return fmt.Errorf("AI_EXECUTION must be immediate or batch")
	}
	c.BatchDeadline = 72 * time.Hour
	if v := os.Getenv("AI_BATCH_DEADLINE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 30*24*time.Hour {
			return fmt.Errorf("AI_BATCH_DEADLINE must be a duration between 1m and 720h")
		}
		c.BatchDeadline = d
	}
	return nil
}

// readAPIKey reads an API key file: one line of at most 4096 bytes.
func readAPIKey(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(raw))
	if key == "" || len(key) > 4096 || strings.ContainsAny(key, "\r\n\x00") {
		return "", fmt.Errorf("the API key must be one line of at most 4096 bytes")
	}
	return key, nil
}

func (c *Config) loadDecisions(models string) error {
	d := decision.Config{Models: os.Getenv("AI_DECISION_MODELS_PATH"), Library: c.OCRLibrary}
	if d.Models == "" {
		path := filepath.Join(models, "laya")
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			d.Models = path
		}
	}
	if d.Models == "" {
		return nil
	}
	for _, n := range []struct {
		key  string
		into *int
	}{
		{"AI_DECISION_THREADS", &d.Threads}, {"AI_DECISION_MAX_TOKENS", &d.MaxTokens}, {"AI_DECISION_HEAD_TOKENS", &d.HeadTokens},
	} {
		if v := os.Getenv(n.key); v != "" {
			parsed, err := strconv.Atoi(v)
			if err != nil || parsed <= 0 {
				return fmt.Errorf("%s must be a positive integer", n.key)
			}
			*n.into = parsed
		}
	}
	if _, err := decision.New(d); err != nil {
		return fmt.Errorf("AI_DECISION: %w", err)
	}
	c.Decisions = d
	return nil
}
