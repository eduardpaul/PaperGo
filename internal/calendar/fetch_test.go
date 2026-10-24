package calendar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"papergo/internal/hosts"
)

func all(string) bool { return true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFetchRejectsUnsafeRedirectsAndOversizedSnapshots(t *testing.T) {
	for _, destination := range []string{"http://example.com/feed", "https://127.0.0.1/feed", "https://[::1]/feed", "https://example.com:8443/feed"} {
		fetcher := NewFetcher()
		calls := 0
		fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{destination}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		})
		if _, err := fetcher.Fetch(context.Background(), "https://example.com/private-source-token", "", "", all); err == nil || strings.Contains(err.Error(), "private-source-token") || calls != 1 {
			t.Fatal(destination, calls, err)
		}
	}
	fetcher := NewFetcher()
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", MaxBytes+1))), Request: req}, nil
	})
	if _, err := fetcher.Fetch(context.Background(), "https://example.com/feed", "", "", all); err == nil {
		t.Fatal("unbounded feed accepted")
	}
}
func TestFetchConditionalValidators(t *testing.T) {
	fetcher := NewFetcher()
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("If-None-Match") != "accepted-version" || req.Header.Get("If-Modified-Since") != "last-time" {
			t.Fatal(req.Header)
		}
		return &http.Response{StatusCode: 304, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	result, err := fetcher.Fetch(context.Background(), "https://example.com/feed", "accepted-version", "last-time", all)
	if err != nil || !result.NotModified {
		t.Fatal(result, err)
	}
}

func TestFetchFollowsTrustedHosts(t *testing.T) {
	trusted := func(host string) bool { return hosts.Trusted([]string{"example.com"}, host) }
	fetcher := NewFetcher()
	calls := 0
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://elsewhere.example/feed"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})
	_, err := fetcher.Fetch(context.Background(), "https://example.com/private-source-token", "", "", trusted)
	if !errors.Is(err, hosts.ErrNotTrusted) || strings.Contains(err.Error(), "private-source-token") || !strings.Contains(err.Error(), "elsewhere.example") || calls != 1 {
		t.Fatal(calls, err)
	}
	calls = 0
	if _, err = fetcher.Fetch(context.Background(), "https://elsewhere.example/feed", "", "", trusted); !errors.Is(err, hosts.ErrNotTrusted) || calls != 0 {
		t.Fatal(calls, err)
	}
	if _, err = ValidateURL(context.Background(), "https://elsewhere.example/feed", trusted); !errors.Is(err, hosts.ErrNotTrusted) {
		t.Fatal(err)
	}
}
