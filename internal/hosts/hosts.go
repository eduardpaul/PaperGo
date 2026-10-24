// Package hosts holds the rules of the organization's trusted hosts: the
// outside hosts PaperGo may fetch from and show without asking. Every server
// feature that fetches from a host a person configured checks it, beneath
// that feature's own rule that fetches reach only public addresses.
package hosts

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"golang.org/x/net/idna"
)

// Any trusts every public host.
const Any = "*"

// MaxEntries bounds the list.
const MaxEntries = 200

// Default is the list of an organization that never changed it.
func Default() []string { return []string{Any} }

// ErrNotTrusted marks a fetch refused because its host is not trusted.
var ErrNotTrusted = errors.New("host is not trusted")

// NotTrusted is the refusal for host, naming it for the person who
// configured the fetch.
func NotTrusted(host string) error {
	return fmt.Errorf("%w: %s isn't a trusted host. Ask an administrator to trust it", ErrNotTrusted, host)
}

var profile = idna.New(idna.MapForLookup(), idna.Transitional(false), idna.BidiRule(), idna.StrictDomainName(true))

// Name returns a host name in the form entries are stored in: lowercase
// ASCII (punycode) without a trailing dot. IP addresses, ports and
// malformed names are refused.
func Name(host string) (string, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" {
		return "", errors.New("a host name is required")
	}
	if _, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		return "", errors.New("IP addresses can't be trusted; use a host name")
	}
	if strings.ContainsAny(host, ":/@?#") {
		return "", errors.New("use a host name without a scheme, port or path")
	}
	ascii, err := profile.ToASCII(host)
	if err != nil || len(ascii) > 253 || !strings.Contains(ascii, ".") {
		return "", fmt.Errorf("%q is not a valid host name", host)
	}
	return strings.ToLower(ascii), nil
}

// Entry returns an entry in stored form: *, a wildcard for the subdomains
// of a host (*.example.com), or a host name.
func Entry(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == Any {
		return Any, nil
	}
	if rest, ok := strings.CutPrefix(entry, "*."); ok {
		name, err := Name(rest)
		if err != nil {
			return "", err
		}
		return "*." + name, nil
	}
	if strings.Contains(entry, "*") {
		return "", fmt.Errorf("%q: a wildcard is * or starts the entry, as in *.example.com", entry)
	}
	return Name(entry)
}

// List returns entries in stored form, without duplicates, in their order.
func List(entries []string) ([]string, error) {
	if len(entries) > MaxEntries {
		return nil, fmt.Errorf("at most %d trusted hosts", MaxEntries)
	}
	out := make([]string, 0, len(entries))
	seen := map[string]bool{}
	for _, raw := range entries {
		entry, err := Entry(raw)
		if err != nil {
			return nil, err
		}
		if !seen[entry] {
			seen[entry] = true
			out = append(out, entry)
		}
	}
	return out, nil
}

// Trusted reports whether entries trust host. A wildcard entry trusts the
// subdomains of its host, not the host itself.
func Trusted(entries []string, host string) bool {
	name, err := Name(host)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry == Any || entry == name {
			return true
		}
		if suffix, ok := strings.CutPrefix(entry, "*"); ok && strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
