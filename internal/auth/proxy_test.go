package auth

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestProxyRequiresPeerSecretAndUnambiguousIdentity(t *testing.T) {
	secret := strings.Repeat("s", 64)
	proxy, err := NewProxy(secret, []netip.Prefix{netip.MustParsePrefix("172.29.242.2/32")}, "https://docs.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, map[string][]string) string
		valid  bool
	}{
		{"valid", nil, true},
		{"untrusted peer", func(_ *testing.T, h map[string][]string) string {
			h["X-Forwarded-For"] = []string{"172.29.242.2"}
			return "172.29.242.3:1234"
		}, false},
		{"missing secret", func(_ *testing.T, h map[string][]string) string { delete(h, ProxySecretHeader); return "" }, false},
		{"wrong secret", func(_ *testing.T, h map[string][]string) string {
			h[ProxySecretHeader] = []string{strings.Repeat("x", 64)}
			return ""
		}, false},
		{"duplicate secret", func(_ *testing.T, h map[string][]string) string {
			h[ProxySecretHeader] = []string{secret, secret}
			return ""
		}, false},
		{"duplicate subject", func(_ *testing.T, h map[string][]string) string {
			h[ProxySubjectHeader] = []string{"alice", "bob"}
			return ""
		}, false},
		{"empty subject", func(_ *testing.T, h map[string][]string) string { h[ProxySubjectHeader] = []string{""}; return "" }, false},
		{"invalid subject", func(_ *testing.T, h map[string][]string) string {
			h[ProxySubjectHeader] = []string{" alice"}
			return ""
		}, false},
		{"forwarded bearer", func(_ *testing.T, h map[string][]string) string {
			h["Authorization"] = []string{"Bearer pgo_attempt"}
			return ""
		}, false},
		{"forwarded basic", func(_ *testing.T, h map[string][]string) string {
			h["Authorization"] = []string{"Basic attempt"}
			return ""
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://docs.example.com/v1/identity/me", nil)
			r.RemoteAddr = "172.29.242.2:1234"
			r.Header.Set(ProxySubjectHeader, "alice")
			r.Header.Set(ProxySecretHeader, secret)
			if tc.mutate != nil {
				if peer := tc.mutate(t, r.Header); peer != "" {
					r.RemoteAddr = peer
				}
			}
			id, err := proxy.Authenticate(r)
			if tc.valid {
				if err != nil || id.Subject != "alice" {
					t.Fatal(id, err)
				}
			} else if err == nil {
				t.Fatal("untrusted identity accepted")
			}
		})
	}
}

func TestProxyRejectsCrossOriginMutations(t *testing.T) {
	secret := strings.Repeat("s", 64)
	p, err := NewProxy(secret, []netip.Prefix{netip.MustParsePrefix("172.29.242.2/32")}, "https://DOCS.example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		origin, referer, site string
		valid                 bool
	}{
		{"https://docs.example.com", "", "same-origin", true},
		{"https://docs.example.com:443", "", "same-origin", true},
		{"", "https://docs.example.com/page", "same-origin", true},
		{"", "", "", true},
		{"https://attacker.example", "", "", false},
		{"https://docs.example.com.attacker.example", "", "", false},
		{"null", "", "", false},
		{"", "https://attacker.example/page", "", false},
		{"https://docs.example.com", "", "cross-site", false},
	} {
		r := httptest.NewRequest("POST", "https://docs.example.com/v1/workspaces", nil)
		r.RemoteAddr = "172.29.242.2:1234"
		r.Header.Set(ProxySecretHeader, secret)
		r.Header.Set(ProxySubjectHeader, "alice")
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		if tc.referer != "" {
			r.Header.Set("Referer", tc.referer)
		}
		if tc.site != "" {
			r.Header.Set("Sec-Fetch-Site", tc.site)
		}
		_, err := p.Authenticate(r)
		if (err == nil) != tc.valid {
			t.Fatalf("origin=%q referer=%q site=%q: %v", tc.origin, tc.referer, tc.site, err)
		}
	}
}
