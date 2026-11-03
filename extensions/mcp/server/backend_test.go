package server

import (
	"net/url"
	"testing"
)

func TestTargetStaysOnTheAPI(t *testing.T) {
	got, err := target("/v1/resources/a%20b/children", url.Values{"$top": {"5"}})
	if err != nil || got != "/v1/resources/a%20b/children?%24top=5" {
		t.Fatalf("%s %v", got, err)
	}
	for _, path := range []string{"/health/ready", "v1/x", "/v1/../admin", "/v1/a/%2e%2e/b", "/v1/a%2Fb", "/v1//x", "/v1/x?y", "/v1/x#y", `/v1/x\y`, "//evil.example/v1/x"} {
		if _, err := target(path, nil); err == nil {
			t.Errorf("%q accepted", path)
		}
	}
}
