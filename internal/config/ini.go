package config

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// iniFile is a parsed config.ini. Password is kept apart so an explicit empty
// admin_password is distinct from a missing key.
type iniFile struct {
	values            map[string]string
	passwordSpecified bool
	password          string
}

func readWorkingINI(getenv func(string) string) (string, error) {
	path := getenv("PAPERGO_CONFIG")
	if path == "" {
		if _, err := os.Stat("config.ini"); err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
		path = "config.ini"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read config file: %w", err)
	}
	return string(raw), nil
}

func parseINI(text string) (iniFile, error) {
	file := iniFile{values: map[string]string{}}
	if strings.TrimSpace(text) == "" {
		return file, nil
	}
	section := ""
	seen := map[string]map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") || strings.TrimSpace(line[1:len(line)-1]) == "" {
				return file, fmt.Errorf("config.ini:%d: invalid section", lineNo)
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return file, fmt.Errorf("config.ini:%d: expected key = value", lineNo)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if section == "" || key == "" {
			return file, fmt.Errorf("config.ini:%d: key outside a section", lineNo)
		}
		if seen[section] == nil {
			seen[section] = map[string]string{}
		}
		seen[section][key] = value
	}
	if err := scanner.Err(); err != nil {
		return file, err
	}
	has := func(section, key string) bool {
		keys := seen[section]
		if keys == nil {
			return false
		}
		_, ok := keys[key]
		return ok
	}
	get := func(section, key string) string {
		if keys := seen[section]; keys != nil {
			return keys[key]
		}
		return ""
	}
	if has("server", "bind") || has("server", "port") {
		bind := "127.0.0.1"
		if has("server", "bind") {
			bind = get("server", "bind")
		}
		port := "8080"
		if has("server", "port") {
			port = get("server", "port")
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || net.ParseIP(bind) == nil {
			return file, fmt.Errorf("server bind and port must be an IP address and a port from 1 to 65535")
		}
		file.values["HTTP_ADDR"] = net.JoinHostPort(bind, port)
	}
	if has("server", "data_dir") {
		dir := get("server", "data_dir")
		if dir == "" || strings.ContainsAny(dir, "\r\n\x00") {
			return file, fmt.Errorf("server data_dir must be a directory path")
		}
		file.values["DATABASE_PATH"] = filepath.Join(dir, "papergo.db")
		file.values["SEARCH_DATABASE_PATH"] = filepath.Join(dir, "search.db")
		if !has("storage", "blob_path") {
			file.values["BLOB_PATH"] = filepath.Join(dir, "blobs")
		}
		if !has("search", "zvec_path") {
			file.values["SEARCH_ZVEC_PATH"] = filepath.Join(dir, "search", "zvec")
		}
	}
	if has("server", "public_url") {
		file.values["PUBLIC_URL"] = get("server", "public_url")
	}
	if has("auth", "mode") {
		file.values["AUTH_MODE"] = get("auth", "mode")
	}
	if has("auth", "admin_user") {
		file.values["ADMIN_SUBJECT"] = get("auth", "admin_user")
	}
	if has("auth", "admin_group") {
		file.values["ADMIN_GROUP"] = get("auth", "admin_group")
	}
	if has("auth", "admin_password") {
		file.passwordSpecified = true
		file.password = get("auth", "admin_password")
	}
	if has("auth", "proxy_secret_file") {
		file.values["PROXY_SECRET_FILE"] = get("auth", "proxy_secret_file")
	}
	if has("auth", "proxy_trusted_cidrs") {
		file.values["PROXY_TRUSTED_CIDRS"] = get("auth", "proxy_trusted_cidrs")
	}
	if has("search", "store") {
		file.values["SEARCH_STORE"] = get("search", "store")
	}
	if has("search", "zvec_path") {
		file.values["SEARCH_ZVEC_PATH"] = get("search", "zvec_path")
	}
	if has("storage", "blob_path") {
		file.values["BLOB_PATH"] = get("storage", "blob_path")
	}
	if has("storage", "max_upload_mb") {
		mb, err := strconv.Atoi(get("storage", "max_upload_mb"))
		if err != nil || mb < 1 || mb > 1024 {
			return file, fmt.Errorf("storage max_upload_mb must be between 1 and 1024")
		}
		file.values["MAX_UPLOAD_BYTES"] = strconv.FormatInt(int64(mb)*1024*1024, 10)
	}
	for _, pair := range []struct{ key, env string }{
		{"issuer", "OIDC_ISSUER"},
		{"audience", "OIDC_AUDIENCE"},
		{"client_id", "OIDC_CLIENT_ID"},
		{"client_scopes", "OIDC_CLIENT_SCOPES"},
		{"groups_claim", "OIDC_GROUPS_CLAIM"},
		{"groups_prefix", "OIDC_GROUPS_PREFIX"},
		{"token_type", "OIDC_TOKEN_TYPE"},
	} {
		if has("oidc", pair.key) {
			file.values[pair.env] = get("oidc", pair.key)
		}
	}
	if has("oidc", "groups_from_userinfo") && strings.EqualFold(get("oidc", "groups_from_userinfo"), "true") {
		file.values["OIDC_GROUPS_FROM_USERINFO"] = "true"
	}
	return file, nil
}
