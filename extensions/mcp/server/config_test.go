package server

import (
	"net/url"
	"testing"
)

func TestParseBackendURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://papergo.example":      true,
		"https://papergo.example/":     true,
		"http://127.0.0.1:8080":        true,
		"http://[::1]:8080":            true,
		"http://localhost:8080":        true,
		"http://papergo:8080":          true,
		"http://10.0.0.5":              true,
		"http://papergo.example":       false,
		"https://papergo.example/api":  false,
		"https://u:p@papergo.example":  false,
		"https://papergo.example?x=1":  false,
		"https://papergo.example#frag": false,
		"https://papergo.example?":     false,
		"ftp://papergo.example":        false,
		"papergo:8080":                 false,
		"":                             false,
	} {
		if _, err := ParseBackendURL(raw); (err == nil) != ok {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

func TestParsePublicURL(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://papergo.example/v1/mcp":   true,
		"http://127.0.0.1:8081/v1/mcp":     true,
		"http://papergo.example/v1/mcp":    false,
		"https://papergo.example/mcp":      false,
		"https://papergo.example/v1/mcp/":  false,
		"https://papergo.example/v1/mcp?a": false,
	} {
		if _, err := ParsePublicURL(raw); (err == nil) != ok {
			t.Errorf("%q: %v", raw, err)
		}
	}
}

func TestParseOrigins(t *testing.T) {
	got, err := ParseOrigins(" https://App.example , http://localhost:5173,")
	if err != nil || len(got) != 2 || got[0] != "https://app.example" || got[1] != "http://localhost:5173" {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"https://app.example/path", "app.example", "https://app.example?x"} {
		if _, err := ParseOrigins(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTargetStaysOnTheAPI(t *testing.T) {
	base, _ := url.Parse("https://papergo.example")
	r := newREST(base, 1<<20, 1)
	got, err := r.target("/v1/resources/a%20b/children", url.Values{"$top": {"5"}})
	if err != nil || got != "https://papergo.example/v1/resources/a%20b/children?%24top=5" {
		t.Fatalf("%s %v", got, err)
	}
	for _, path := range []string{"/health/ready", "v1/x", "/v1/../admin", "/v1/a/%2e%2e/b", "/v1/a%2Fb", "/v1//x", "/v1/x?y", "/v1/x#y", `/v1/x\y`, "//evil.example/v1/x"} {
		if _, err := r.target(path, nil); err == nil {
			t.Errorf("%q accepted", path)
		}
	}
}
