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

type yamlConfig struct {
	Server struct {
		Bind      *string `yaml:"bind"`
		Port      *int    `yaml:"port"`
		DataDir   *string `yaml:"data_dir"`
		PublicURL string  `yaml:"public_url"`
	} `yaml:"server"`
	Auth struct {
		Mode              string    `yaml:"mode"`
		AdminUser         string    `yaml:"admin_user"`
		AdminGroup        string    `yaml:"admin_group"`
		AdminPassword     yaml.Node `yaml:"admin_password"`
		ProxySecretFile   string    `yaml:"proxy_secret_file"`
		ProxyTrustedCIDRs []string  `yaml:"proxy_trusted_cidrs"`
	} `yaml:"auth"`
	Search struct {
		Store    string `yaml:"store"`
		ZvecPath string `yaml:"zvec_path"`
	} `yaml:"search"`
	Storage struct {
		BlobPath    string `yaml:"blob_path"`
		MaxUploadMB *int   `yaml:"max_upload_mb"`
	} `yaml:"storage"`
	OIDC struct {
		Issuer             string `yaml:"issuer"`
		Audience           string `yaml:"audience"`
		ClientID           string `yaml:"client_id"`
		ClientScopes       string `yaml:"client_scopes"`
		GroupsClaim        string `yaml:"groups_claim"`
		GroupsPrefix       string `yaml:"groups_prefix"`
		TokenType          string `yaml:"token_type"`
		GroupsFromUserInfo bool   `yaml:"groups_from_userinfo"`
	} `yaml:"oidc"`
}

type yamlFile struct {
	values            map[string]string
	passwordSpecified bool
	password          string
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
		return "", fmt.Errorf("read config file: %w", err)
	}
	return string(raw), nil
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
	file.values = map[string]string{
		"PUBLIC_URL": in.Server.PublicURL, "AUTH_MODE": in.Auth.Mode,
		"ADMIN_SUBJECT": in.Auth.AdminUser, "ADMIN_GROUP": in.Auth.AdminGroup,
		"PROXY_SECRET_FILE": in.Auth.ProxySecretFile, "PROXY_TRUSTED_CIDRS": strings.Join(in.Auth.ProxyTrustedCIDRs, ","),
		"SEARCH_STORE": in.Search.Store, "SEARCH_ZVEC_PATH": in.Search.ZvecPath, "BLOB_PATH": in.Storage.BlobPath,
		"OIDC_ISSUER": in.OIDC.Issuer, "OIDC_AUDIENCE": in.OIDC.Audience,
		"OIDC_CLIENT_ID": in.OIDC.ClientID, "OIDC_CLIENT_SCOPES": in.OIDC.ClientScopes,
		"OIDC_GROUPS_CLAIM": in.OIDC.GroupsClaim, "OIDC_GROUPS_PREFIX": in.OIDC.GroupsPrefix,
		"OIDC_TOKEN_TYPE": in.OIDC.TokenType, "OIDC_GROUPS_FROM_USERINFO": strconv.FormatBool(in.OIDC.GroupsFromUserInfo),
	}
	if in.Server.Bind != nil || in.Server.Port != nil {
		bind, port := "127.0.0.1", 8080
		if in.Server.Bind != nil {
			bind = *in.Server.Bind
		}
		if in.Server.Port != nil {
			port = *in.Server.Port
		}
		if net.ParseIP(bind) == nil || port < 1 || port > 65535 {
			return file, fmt.Errorf("server bind and port must be an IP address and a port from 1 to 65535")
		}
		file.values["HTTP_ADDR"] = net.JoinHostPort(bind, strconv.Itoa(port))
	}
	if in.Server.DataDir != nil {
		dir := *in.Server.DataDir
		if dir == "" || strings.ContainsAny(dir, "\r\n\x00") {
			return file, fmt.Errorf("server data_dir must be a directory path")
		}
		file.values["DATABASE_PATH"] = filepath.Join(dir, "papergo.db")
		file.values["SEARCH_DATABASE_PATH"] = filepath.Join(dir, "search.db")
		if in.Storage.BlobPath == "" {
			file.values["BLOB_PATH"] = filepath.Join(dir, "blobs")
		}
		if in.Search.ZvecPath == "" {
			file.values["SEARCH_ZVEC_PATH"] = filepath.Join(dir, "search", "zvec")
		}
	}
	if in.Storage.MaxUploadMB != nil {
		mb := *in.Storage.MaxUploadMB
		if mb < 1 || mb > 1024 {
			return file, fmt.Errorf("storage max_upload_mb must be between 1 and 1024")
		}
		file.values["MAX_UPLOAD_BYTES"] = strconv.FormatInt(int64(mb)*1024*1024, 10)
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
