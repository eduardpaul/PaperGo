package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env          string
	Address      string
	DatabasePath string
	BlobPath     string
	AuthMode     string
	DevToken     string
	DevSubject   string
	Issuer       string
	Audience     string
	MaxUpload    int64
	// MaxInFlight bounds concurrent API requests; excess requests are shed with 503.
	MaxInFlight int
	// RequestTimeout cancels API work that outlives it; blob transfers are exempt.
	RequestTimeout time.Duration
}

func Load() (Config, error) {
	c := Config{Env: value("APP_ENV", "development"), Address: value("HTTP_ADDR", "127.0.0.1:8080"), DatabasePath: value("DATABASE_PATH", "data/papergo.db"), BlobPath: value("BLOB_PATH", "data/blobs"), AuthMode: value("AUTH_MODE", "oidc"), DevToken: os.Getenv("DEV_TOKEN"), DevSubject: value("DEV_SUBJECT", "local-admin"), Issuer: os.Getenv("OIDC_ISSUER"), Audience: os.Getenv("OIDC_AUDIENCE"), MaxUpload: 64 * 1024 * 1024, MaxInFlight: 32, RequestTimeout: 30 * time.Second}
	if v := os.Getenv("MAX_UPLOAD_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > 1024*1024*1024 {
			return c, fmt.Errorf("MAX_UPLOAD_BYTES must be between 1 and 1073741824")
		}
		c.MaxUpload = n
	}
	if v := os.Getenv("MAX_IN_FLIGHT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10000 {
			return c, fmt.Errorf("MAX_IN_FLIGHT must be between 1 and 10000")
		}
		c.MaxInFlight = n
	}
	if v := os.Getenv("REQUEST_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second || d > 10*time.Minute {
			return c, fmt.Errorf("REQUEST_TIMEOUT must be a duration between 1s and 10m")
		}
		c.RequestTimeout = d
	}
	if c.Env != "development" && c.Env != "test" && c.Env != "production" {
		return c, fmt.Errorf("invalid APP_ENV")
	}
	switch c.AuthMode {
	case "development":
		if c.Env == "production" {
			return c, fmt.Errorf("production requires OIDC authentication")
		}
		if len(c.DevToken) < 32 {
			return c, fmt.Errorf("DEV_TOKEN requires at least 32 characters")
		}
		if strings.TrimSpace(c.DevSubject) == "" || len(c.DevSubject) > 255 {
			return c, fmt.Errorf("invalid DEV_SUBJECT")
		}
	case "oidc":
		if c.Issuer == "" || c.Audience == "" {
			return c, fmt.Errorf("OIDC_ISSUER and OIDC_AUDIENCE are required")
		}
		if c.Env == "production" && !strings.HasPrefix(c.Issuer, "https://") {
			return c, fmt.Errorf("production OIDC_ISSUER requires HTTPS")
		}
	default:
		return c, fmt.Errorf("AUTH_MODE must be development or oidc")
	}
	return c, nil
}
func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
