package config

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultsFile is the file beside the executable that holds an
// installation's own defaults, such as the container image's listen
// address and data directory. config.yaml overrides it, and environment
// variables override both.
const DefaultsFile = "defaults.yaml"

// yamlConfig is config.yaml. Every setting PaperGo reads has a key here and
// an environment variable (settings and derivedKeys): a value is a string,
// validated as its environment variable is, unless the key says otherwise.
type yamlConfig struct {
	Server struct {
		Bind           *string `yaml:"bind"`
		Port           *int    `yaml:"port"`
		DataDir        *string `yaml:"data_dir"`
		PublicURL      string  `yaml:"public_url"`
		Environment    string  `yaml:"environment"`
		MaxInFlight    string  `yaml:"max_in_flight"`
		RequestTimeout string  `yaml:"request_timeout"`
	} `yaml:"server"`
	Database struct {
		URL string `yaml:"url"`
	} `yaml:"database"`
	Auth struct {
		Mode              string    `yaml:"mode"`
		AdminUser         string    `yaml:"admin_user"`
		AdminGroup        string    `yaml:"admin_group"`
		AdminPassword     yaml.Node `yaml:"admin_password"`
		AdminPasswordFile string    `yaml:"admin_password_file"`
		DevToken          string    `yaml:"dev_token"`
		DevSubject        string    `yaml:"dev_subject"`
		ProxySecretFile   string    `yaml:"proxy_secret_file"`
		ProxyTrustedCIDRs []string  `yaml:"proxy_trusted_cidrs"`
	} `yaml:"auth"`
	Search struct {
		Store        string   `yaml:"store"`
		DatabasePath string   `yaml:"database_path"`
		Languages    []string `yaml:"languages"`
	} `yaml:"search"`
	Comments string `yaml:"comments"`
	Storage  struct {
		BlobPath    string `yaml:"blob_path"`
		MaxUploadMB *int   `yaml:"max_upload_mb"`
		EditMaxMB   *int   `yaml:"edit_max_mb"`
	} `yaml:"storage"`
	OIDC struct {
		Issuer             string `yaml:"issuer"`
		Audience           string `yaml:"audience"`
		ClientID           string `yaml:"client_id"`
		ClientScopes       string `yaml:"client_scopes"`
		GroupsClaim        string `yaml:"groups_claim"`
		GroupsPrefix       string `yaml:"groups_prefix"`
		TokenType          string `yaml:"token_type"`
		GroupsFromUserInfo string `yaml:"groups_from_userinfo"`
	} `yaml:"oidc"`
	MCP struct {
		Enabled    *bool    `yaml:"enabled"`
		Extensions []string `yaml:"extensions"`
	} `yaml:"mcp"`
	ONNXRuntime struct {
		Library string `yaml:"library"`
	} `yaml:"onnxruntime"`
	OCR struct {
		Models  string `yaml:"models"`
		Threads string `yaml:"threads"`
	} `yaml:"ocr"`
	AI struct {
		Execution string `yaml:"execution"`
		Chat      struct {
			Provider   string `yaml:"provider"`
			Endpoint   string `yaml:"endpoint"`
			Model      string `yaml:"model"`
			APIKeyFile string `yaml:"api_key_file"`
		} `yaml:"chat"`
		Batch struct {
			Provider   string `yaml:"provider"`
			Endpoint   string `yaml:"endpoint"`
			Model      string `yaml:"model"`
			APIKeyFile string `yaml:"api_key_file"`
			MaxTokens  string `yaml:"max_tokens"`
			Deadline   string `yaml:"deadline"`
		} `yaml:"batch"`
		Embeddings struct {
			Provider   string `yaml:"provider"`
			Model      string `yaml:"model"`
			ModelsPath string `yaml:"models_path"`
			Endpoint   string `yaml:"endpoint"`
			APIKeyFile string `yaml:"api_key_file"`
			Dimensions string `yaml:"dimensions"`
			Threads    string `yaml:"threads"`
			MaxTokens  string `yaml:"max_tokens"`
		} `yaml:"embeddings"`
		Decisions struct {
			ModelsPath string `yaml:"models_path"`
			Threads    string `yaml:"threads"`
			MaxTokens  string `yaml:"max_tokens"`
			HeadTokens string `yaml:"head_tokens"`
		} `yaml:"decisions"`
	} `yaml:"ai"`
	Retention struct {
		Runs        string `yaml:"runs"`
		SystemRuns  string `yaml:"system_runs"`
		ResultCache string `yaml:"result_cache"`
	} `yaml:"retention"`
	Quotas struct {
		PerCreator            string `yaml:"per_creator"`
		PerWorkspace          string `yaml:"per_workspace"`
		Installation          string `yaml:"installation"`
		WorkspacePayloadBytes string `yaml:"workspace_payload_bytes"`
	} `yaml:"quotas"`
	Organization struct {
		Locale            string `yaml:"locale"`
		Language          string `yaml:"language"`
		TimeZone          string `yaml:"time_zone"`
		DateFormat        string `yaml:"date_format"`
		TimeFormat        string `yaml:"time_format"`
		NumberFormat      string `yaml:"number_format"`
		Theme             string `yaml:"theme"`
		DefaultCurrency   string `yaml:"default_currency"`
		DocumentLanguages string `yaml:"document_languages"`
	} `yaml:"organization"`
	Workers map[string]Worker `yaml:"workers"`
}

// yamlSetting is a config.yaml value and the environment variable that
// overrides it.
type yamlSetting struct{ env, value string }

// settings are the config.yaml values that map one to one onto an
// environment variable.
func (in *yamlConfig) settings() []yamlSetting {
	return []yamlSetting{
		{"PUBLIC_URL", in.Server.PublicURL}, {"APP_ENV", in.Server.Environment},
		{"MAX_IN_FLIGHT", in.Server.MaxInFlight}, {"REQUEST_TIMEOUT", in.Server.RequestTimeout},
		{"DATABASE_URL", in.Database.URL},
		{"AUTH_MODE", in.Auth.Mode}, {"ADMIN_SUBJECT", in.Auth.AdminUser}, {"ADMIN_GROUP", in.Auth.AdminGroup},
		{"ADMIN_PASSWORD_FILE", in.Auth.AdminPasswordFile}, {"DEV_TOKEN", in.Auth.DevToken}, {"DEV_SUBJECT", in.Auth.DevSubject},
		{"PROXY_SECRET_FILE", in.Auth.ProxySecretFile}, {"PROXY_TRUSTED_CIDRS", strings.Join(in.Auth.ProxyTrustedCIDRs, ",")},
		{"SEARCH_STORE", in.Search.Store}, {"SEARCH_DATABASE_PATH", in.Search.DatabasePath},
		{"SEARCH_LANGUAGES", strings.Join(in.Search.Languages, ",")},
		{"COMMENTS", in.Comments}, {"BLOB_PATH", in.Storage.BlobPath},
		{"OIDC_ISSUER", in.OIDC.Issuer}, {"OIDC_AUDIENCE", in.OIDC.Audience},
		{"OIDC_CLIENT_ID", in.OIDC.ClientID}, {"OIDC_CLIENT_SCOPES", in.OIDC.ClientScopes},
		{"OIDC_GROUPS_CLAIM", in.OIDC.GroupsClaim}, {"OIDC_GROUPS_PREFIX", in.OIDC.GroupsPrefix},
		{"OIDC_TOKEN_TYPE", in.OIDC.TokenType}, {"OIDC_GROUPS_FROM_USERINFO", in.OIDC.GroupsFromUserInfo},
		{"MCP_EXTENSIONS", strings.Join(in.MCP.Extensions, ",")},
		{"ONNXRUNTIME_LIB", in.ONNXRuntime.Library},
		{"OCR_MODELS", in.OCR.Models}, {"OCR_THREADS", in.OCR.Threads},
		{"AI_EXECUTION", in.AI.Execution},
		{"AI_CHAT_PROVIDER", in.AI.Chat.Provider}, {"AI_CHAT_ENDPOINT", in.AI.Chat.Endpoint},
		{"AI_CHAT_MODEL", in.AI.Chat.Model}, {"AI_CHAT_API_KEY_FILE", in.AI.Chat.APIKeyFile},
		{"AI_BATCH_PROVIDER", in.AI.Batch.Provider}, {"AI_BATCH_ENDPOINT", in.AI.Batch.Endpoint},
		{"AI_BATCH_MODEL", in.AI.Batch.Model}, {"AI_BATCH_API_KEY_FILE", in.AI.Batch.APIKeyFile},
		{"AI_BATCH_MAX_TOKENS", in.AI.Batch.MaxTokens}, {"AI_BATCH_DEADLINE", in.AI.Batch.Deadline},
		{"AI_EMBEDDINGS_PROVIDER", in.AI.Embeddings.Provider}, {"AI_EMBEDDINGS_MODEL", in.AI.Embeddings.Model},
		{"AI_EMBEDDINGS_MODELS_PATH", in.AI.Embeddings.ModelsPath}, {"AI_EMBEDDINGS_ENDPOINT", in.AI.Embeddings.Endpoint},
		{"AI_EMBEDDINGS_API_KEY_FILE", in.AI.Embeddings.APIKeyFile}, {"AI_EMBEDDINGS_DIMENSIONS", in.AI.Embeddings.Dimensions},
		{"AI_EMBEDDINGS_THREADS", in.AI.Embeddings.Threads}, {"AI_EMBEDDINGS_MAX_TOKENS", in.AI.Embeddings.MaxTokens},
		{"AI_DECISION_MODELS_PATH", in.AI.Decisions.ModelsPath}, {"AI_DECISION_THREADS", in.AI.Decisions.Threads},
		{"AI_DECISION_MAX_TOKENS", in.AI.Decisions.MaxTokens}, {"AI_DECISION_HEAD_TOKENS", in.AI.Decisions.HeadTokens},
		{"RUN_RETENTION", in.Retention.Runs}, {"SYSTEM_RUN_RETENTION", in.Retention.SystemRuns},
		{"RESULT_CACHE_TTL", in.Retention.ResultCache},
		{"OPERATION_QUOTA_PER_CREATOR", in.Quotas.PerCreator}, {"OPERATION_QUOTA_PER_WORKSPACE", in.Quotas.PerWorkspace},
		{"OPERATION_QUOTA_INSTALLATION", in.Quotas.Installation},
		{"OPERATION_QUOTA_WORKSPACE_PAYLOAD_BYTES", in.Quotas.WorkspacePayloadBytes},
		{"ORGANIZATION_LOCALE", in.Organization.Locale}, {"ORGANIZATION_LANGUAGE", in.Organization.Language},
		{"ORGANIZATION_TIME_ZONE", in.Organization.TimeZone}, {"ORGANIZATION_DATE_FORMAT", in.Organization.DateFormat},
		{"ORGANIZATION_TIME_FORMAT", in.Organization.TimeFormat}, {"ORGANIZATION_NUMBER_FORMAT", in.Organization.NumberFormat},
		{"ORGANIZATION_THEME", in.Organization.Theme}, {"ORGANIZATION_DEFAULT_CURRENCY", in.Organization.DefaultCurrency},
		{"ORGANIZATION_DOCUMENT_LANGUAGES", in.Organization.DocumentLanguages},
	}
}

// derivedKeys are the environment variables that config.yaml sets through
// a key of its own shape: server bind and port make HTTP_ADDR, data_dir
// the database path, megabytes the byte limits, mcp.enabled MCP, and
// admin_password the initial password.
var derivedKeys = []string{"HTTP_ADDR", "DATABASE_PATH", "MAX_UPLOAD_BYTES", "FILE_EDIT_MAX_BYTES", "MCP", "ADMIN_PASSWORD"}

// Worker adjusts a named worker of the operation runtime. Fields left out
// keep the executable's default.
type Worker struct {
	Concurrency *int             `yaml:"concurrency"`
	Admission   *WorkerAdmission `yaml:"admission"`
	Window      *WorkerWindow    `yaml:"window"`
}

// WorkerAdmission is when a worker's queued work becomes eligible.
type WorkerAdmission struct {
	Policy    string       `yaml:"policy"`
	Threshold int          `yaml:"threshold"`
	MaxWait   string       `yaml:"max_wait"`
	Batch     int          `yaml:"batch"`
	Flush     *WorkerFlush `yaml:"flush"`
}

// WorkerFlush is a schedule that makes every group of a worker eligible.
type WorkerFlush struct {
	Cron     string `yaml:"cron"`
	TimeZone string `yaml:"time_zone"`
	Missed   string `yaml:"missed"`
}

// WorkerWindow is the daily time a worker starts executions in.
type WorkerWindow struct {
	TimeZone string `yaml:"time_zone"`
	From     string `yaml:"from"`
	To       string `yaml:"to"`
}

// yamlFile is one YAML layer, or the merge of several. bind, port and
// dataDir stay apart until the layers are merged, so that a later layer
// setting only the port keeps an earlier layer's bind address.
type yamlFile struct {
	values            map[string]string
	bind, port        string
	dataDir           string
	workers           map[string]Worker
	passwordSpecified bool
	password          string
}

// readLayers reads the YAML layers Load applies, in order: DefaultsFile
// beside the executable, when present, then config.yaml from PAPERGO_CONFIG
// or the working directory.
func readLayers(getenv func(string) string, directory string) ([]string, error) {
	var layers []string
	raw, err := os.ReadFile(filepath.Join(directory, DefaultsFile))
	switch {
	case err == nil:
		layers = append(layers, string(raw))
	case !os.IsNotExist(err):
		return nil, fmt.Errorf("read %s: %w", DefaultsFile, explainPermission(filepath.Join(directory, DefaultsFile), err))
	}
	text, err := readWorkingYAML(getenv)
	if err != nil {
		return nil, err
	}
	return append(layers, text), nil
}

func readWorkingYAML(getenv func(string) string) (string, error) {
	path := getenv("PAPERGO_CONFIG")
	explicit := path != ""
	if !explicit {
		path = "config.yaml"
	}
	raw, err := os.ReadFile(path)
	if !explicit && os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read config file: %w", explainPermission(path, err))
	}
	return string(raw), nil
}

// parseLayers parses and merges YAML layers; a later layer's settings
// replace an earlier one's.
func parseLayers(layers []string) (yamlFile, error) {
	merged := yamlFile{values: map[string]string{}}
	for _, text := range layers {
		layer, err := parseYAML(text)
		if err != nil {
			return merged, err
		}
		for k, v := range layer.values {
			if v != "" {
				merged.values[k] = v
			}
		}
		for _, field := range []struct {
			into *string
			from string
		}{{&merged.bind, layer.bind}, {&merged.port, layer.port}, {&merged.dataDir, layer.dataDir}} {
			if field.from != "" {
				*field.into = field.from
			}
		}
		for name, w := range layer.workers {
			if merged.workers == nil {
				merged.workers = map[string]Worker{}
			}
			merged.workers[name] = w
		}
		if layer.passwordSpecified {
			merged.passwordSpecified, merged.password = true, layer.password
		}
	}
	if merged.bind != "" || merged.port != "" {
		bind, port := "127.0.0.1", "8080"
		if merged.bind != "" {
			bind = merged.bind
		}
		if merged.port != "" {
			port = merged.port
		}
		merged.values["HTTP_ADDR"] = net.JoinHostPort(bind, port)
	}
	if dir := merged.dataDir; dir != "" {
		merged.values["DATABASE_PATH"] = filepath.Join(dir, "papergo.db")
		for key, name := range map[string]string{"SEARCH_DATABASE_PATH": "search.db", "BLOB_PATH": "blobs"} {
			if merged.values[key] == "" {
				merged.values[key] = filepath.Join(dir, name)
			}
		}
	}
	return merged, nil
}

func parseYAML(text string) (yamlFile, error) {
	file := yamlFile{values: map[string]string{}}
	var in yamlConfig
	decoder := yaml.NewDecoder(strings.NewReader(text))
	decoder.KnownFields(true)
	if err := decoder.Decode(&in); err == io.EOF {
		return file, nil
	} else if err != nil {
		return file, fmt.Errorf("config.yaml: %w", err)
	}
	if err := decoder.Decode(new(yamlConfig)); err != io.EOF {
		return file, fmt.Errorf("config.yaml must contain one YAML document")
	}
	file.workers = in.Workers
	for _, s := range in.settings() {
		file.values[s.env] = s.value
	}
	if in.MCP.Enabled != nil {
		file.values["MCP"] = map[bool]string{true: "on", false: "off"}[*in.MCP.Enabled]
	}
	if in.Server.Bind != nil {
		if net.ParseIP(*in.Server.Bind) == nil {
			return file, fmt.Errorf("server bind must be an IP address")
		}
		file.bind = *in.Server.Bind
	}
	if in.Server.Port != nil {
		if *in.Server.Port < 1 || *in.Server.Port > 65535 {
			return file, fmt.Errorf("server port must be a port from 1 to 65535")
		}
		file.port = strconv.Itoa(*in.Server.Port)
	}
	if in.Server.DataDir != nil {
		dir := *in.Server.DataDir
		if dir == "" || strings.ContainsAny(dir, "\r\n\x00") {
			return file, fmt.Errorf("server data_dir must be a directory path")
		}
		file.dataDir = dir
	}
	if in.Storage.MaxUploadMB != nil {
		mb := *in.Storage.MaxUploadMB
		if mb < 1 || mb > 1024 {
			return file, fmt.Errorf("storage max_upload_mb must be between 1 and 1024")
		}
		file.values["MAX_UPLOAD_BYTES"] = strconv.FormatInt(int64(mb)*1024*1024, 10)
	}
	if in.Storage.EditMaxMB != nil {
		mb := *in.Storage.EditMaxMB
		if mb < 1 || mb > 1024 {
			return file, fmt.Errorf("storage edit_max_mb must be between 1 and 1024")
		}
		file.values["FILE_EDIT_MAX_BYTES"] = strconv.FormatInt(int64(mb)*1024*1024, 10)
	}
	if password := in.Auth.AdminPassword; password.Kind != 0 {
		file.passwordSpecified = true
		if password.Tag != "!!null" {
			if password.Kind != yaml.ScalarNode || password.Tag != "!!str" {
				return file, fmt.Errorf("auth admin_password must be a string or null")
			}
			file.password = password.Value
		}
	}
	return file, nil
}
