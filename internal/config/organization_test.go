package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestOrganizationLocalePreset(t *testing.T) {
	c, err := LoadFrom(nil, "organization:\n  locale: es-ES\n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"language": "es-ES", "number_format": "es-ES", "time_zone": "Europe/Madrid", "date_format": "dd/MM/yyyy",
		"time_format": "24h", "default_currency": "EUR", "document_languages": "spa+eng"}
	for k, v := range want {
		if c.OrganizationPreferences[k] != v {
			t.Errorf("%s = %q, want %q", k, c.OrganizationPreferences[k], v)
		}
	}
	if strings.Join(c.SearchLanguages, ",") != "spanish,english" {
		t.Fatalf("search %v", c.SearchLanguages)
	}
	// What is named replaces the locale's.
	c, err = LoadFrom(func(key string) string {
		return map[string]string{"ORGANIZATION_TIME_ZONE": "Atlantic/Canary", "SEARCH_LANGUAGES": "spanish"}[key]
	}, "organization:\n  locale: es-es\n  default_currency: USD\n")
	if err != nil || c.OrganizationPreferences["time_zone"] != "Atlantic/Canary" || c.OrganizationPreferences["default_currency"] != "USD" || strings.Join(c.SearchLanguages, ",") != "spanish" {
		t.Fatalf("overrides %v search %v %v", c.OrganizationPreferences, c.SearchLanguages, err)
	}
	// Every preset holds values the preferences accept.
	for _, name := range LocalePresets() {
		if _, err := LoadFrom(func(key string) string {
			return map[string]string{"ORGANIZATION_LOCALE": name}[key]
		}, ""); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	c, err = LoadFrom(nil, "")
	if err != nil || c.OrganizationPreferences != nil || strings.Join(c.SearchLanguages, ",") != "english" {
		t.Fatalf("no locale %v %v %v", c.OrganizationPreferences, c.SearchLanguages, err)
	}
	for name, text := range map[string]string{
		"unknown locale":  "organization:\n  locale: xx-XX\n",
		"bad time zone":   "organization:\n  time_zone: Mars/Olympus\n",
		"bad currency":    "organization:\n  default_currency: euro\n",
		"bad date format": "organization:\n  date_format: yyyy\n",
	} {
		if _, err := LoadFrom(nil, text); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestPermissionErrorsSayWhoNeedsAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	err := ExplainPermission(&fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission})
	if !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "PaperGo runs as user") {
		t.Fatalf("%v", err)
	}
	other := &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	if ExplainPermission(other) != error(other) {
		t.Fatal("a missing file was explained as a permission error")
	}
}
