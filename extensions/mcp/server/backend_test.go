package server

import (
	"errors"
	"net/http"
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

// A handler writing past the limit is told to stop.
func TestRecorderStopsAtTheLimit(t *testing.T) {
	r := &recorder{header: http.Header{}, max: 8}
	if n, err := r.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := r.Write([]byte("6789")); n != 3 || !errors.Is(err, errTooLarge) || !r.over {
		t.Fatal(n, err, r.over)
	}
	if n, err := r.Write([]byte("0")); n != 0 || !errors.Is(err, errTooLarge) || r.body.String() != "12345678" {
		t.Fatal(n, err, r.body.String())
	}
}
