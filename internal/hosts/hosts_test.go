package hosts

import (
	"errors"
	"slices"
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
	if Trusted(Default(), "127.0.0.1") {
		t.Fatal("* trusts an address")
	}
	if err := NotTrusted("example.org"); !errors.Is(err, ErrNotTrusted) {
		t.Fatal("NotTrusted is not ErrNotTrusted")
	}
}
