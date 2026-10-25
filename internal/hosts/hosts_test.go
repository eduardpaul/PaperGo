package hosts

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestEntriesAreStoredInOneForm(t *testing.T) {
	got, err := List([]string{" Docs.Example.COM. ", "*.SharePoint.com", "*", "bücher.example", "docs.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"docs.example.com", "*.sharepoint.com", "*", "xn--bcher-kva.example"}
	if !slices.Equal(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	for _, bad := range []string{"", "10.0.0.1", "[::1]", "example.com:8443", "https://example.com", "a.*.example.com", "*example.com", "localhost", "exa mple.com"} {
		if _, err := Entry(bad); err == nil {
			t.Errorf("Entry(%q) accepted", bad)
		}
	}
	if _, err := List(make([]string, MaxEntries+1)); err == nil {
		t.Fatal("List accepted more than MaxEntries")
	}
}

func TestTrustedMatchesHostsAndSubdomains(t *testing.T) {
	entries := []string{"docs.example.com", "*.sharepoint.com", "xn--bcher-kva.example"}
	for host, want := range map[string]bool{
		"docs.example.com":         true,
		"DOCS.example.com.":        true,
		"other.example.com":        false,
		"contoso.sharepoint.com":   true,
		"a.b.sharepoint.com":       true,
		"sharepoint.com":           false,
		"evilsharepoint.com":       false,
		"bücher.example":           true,
		"docs.example.com.evil.io": false,
		"10.0.0.1":                 false,
	} {
		if got := Trusted(entries, host); got != want {
			t.Errorf("Trusted(%q) = %v, want %v", host, got, want)
		}
	}
	if !Trusted(Default(), "anything.example") {
		t.Fatal("the default list does not trust every host")
	}
	// Only * trusts hosts named by an IP address; fetches keep to public addresses on their own.
	for _, host := range []string{"203.0.113.5", "[2001:db8::1]"} {
		if !Trusted(Default(), host) || Trusted(entries, host) {
			t.Errorf("Trusted(%q): * should trust it, and only *", host)
		}
	}
	if err := NotTrusted("example.org"); !errors.Is(err, ErrNotTrusted) {
		t.Fatal("NotTrusted is not ErrNotTrusted")
	}
	if err := NotTrusted("203.0.113.5"); !errors.Is(err, ErrNotTrusted) || !strings.Contains(err.Error(), "only * trusts") {
		t.Fatalf("NotTrusted for an address: %v", err)
	}
	for host, want := range map[string]string{"Docs.Example.com.": "docs.example.com", "[2001:DB8::1]": "2001:db8::1", "203.0.113.5": "203.0.113.5", "localhost": ""} {
		if got := Key(host); got != want {
			t.Errorf("Key(%q) = %q, want %q", host, got, want)
		}
	}
}
