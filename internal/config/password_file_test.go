package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionRequiresConfiguredInitialPassword(t *testing.T) {
	env := map[string]string{"APP_ENV": "production", "AUTH_MODE": "local"}
	getenv := func(key string) string { return env[key] }
	c, err := LoadFrom(getenv, "")
	if err != nil || c.AdminPassword != "" || !c.AdminPasswordOmitted {
		t.Fatalf("production supplied a default password: %+v %v", c, err)
	}
	env["ADMIN_PASSWORD"] = "configured-password"
	c, err = LoadFrom(getenv, "")
	if err != nil || c.AdminPassword != env["ADMIN_PASSWORD"] || c.AdminPasswordOmitted {
		t.Fatal("explicit password was not selected", err)
	}
}

func TestPasswordFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	env := map[string]string{"APP_ENV": "production", "AUTH_MODE": "local", "ADMIN_PASSWORD_FILE": path}
	getenv := func(key string) string { return env[key] }
	for _, content := range []string{" secret with spaces \r\n", strings.Repeat("x", 72) + "\n"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadFrom(getenv, "auth:\n  admin_password: yaml-password\n")
		if err != nil || c.AdminPassword != strings.TrimRight(content, "\r\n") {
			t.Fatalf("secret file not respected: %q %v", c.AdminPassword, err)
		}
	}
	for _, content := range []string{"", "\n", strings.Repeat("x", 73), strings.Repeat("x", 1000), "x" + strings.Repeat("\n", 74) + "hidden-suffix"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFrom(getenv, ""); err == nil {
			t.Fatal("invalid password file accepted")
		}
	}
	env["ADMIN_PASSWORD"] = "environment-password"
	if _, err := LoadFrom(getenv, ""); err == nil {
		t.Fatal("ambiguous password configuration accepted")
	}
	delete(env, "ADMIN_PASSWORD")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFrom(getenv, ""); err == nil {
		t.Fatal("missing password file accepted")
	}
}
