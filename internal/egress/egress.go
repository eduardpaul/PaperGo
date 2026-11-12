// Package egress decides which outside addresses PaperGo connects to and
// makes those connections. Public addresses are reached on hosts the
// organization trusts; any other address only inside the networks the
// operator lists. Some addresses are never reached. Every connection checks
// the address it dials, after resolving the host, so a name that resolves
// somewhere else is refused at connect time rather than when it was saved.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"papergo/internal/hosts"
)

// denied are the ranges that are not public, beyond what netip classifies:
// shared, documentation, benchmarking, translation and reserved space.
var denied = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/3"), netip.MustParsePrefix("::/96"), netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"), netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"), netip.MustParsePrefix("ff00::/8"),
}

// never are the ranges no setting opens: link-local (where cloud metadata
// services answer), multicast and unspecified addresses.
var never = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("::/128"), netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"), netip.MustParsePrefix("fd00:ec2::254/128"),
}

// Public reports whether ip is a public internet address.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range denied {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Never reports whether ip is an address no setting opens.
func Never(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range never {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrUnreachable is an address PaperGo may never connect to.
var ErrUnreachable = errors.New("this address is never reachable from PaperGo")

// NetworkError refuses a non-public address outside the operator's
// networks. It names the address and a network that would allow it, so the
// message says what to ask for.
type NetworkError struct {
	Address netip.Addr
	Suggest netip.Prefix
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("%s is on a private network; ask the operator to add %s to connections.networks", e.Address, e.Suggest)
}

// suggest is the network an operator would add for ip: its /24, or /64.
func suggest(ip netip.Addr) netip.Prefix {
	bits := 24
	if ip.Is6() {
		bits = 64
	}
	p, _ := ip.Prefix(bits)
	return p
}

// Policy is what one kind of connection may reach.
type Policy struct {
	// Networks are the non-public networks the operator opened, beyond
	// which only public addresses are reached. Empty opens none.
	Networks []netip.Prefix
	// Trusted reports whether the organization trusts a host; it decides
	// public destinations. Nil trusts none.
	Trusted func(host string) bool
	// RequirePrivate refuses every public address: a connection without
	// TLS reaches only the operator's networks.
	RequirePrivate bool
}

// ErrNeedsTLS refuses a connection without TLS to a public address.
var ErrNeedsTLS = errors.New("a public address is reached only over TLS (mqtts, wss or https)")

// Private reports whether ip is reached as a private address: inside the
// operator's networks.
func (p Policy) Private(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, n := range p.Networks {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Allow says why ip may not be dialled for host, or nil when it may.
// A private address in the operator's networks needs no trust; a public
// one needs host to be trusted.
func (p Policy) Allow(host string, ip netip.Addr) error {
	ip = ip.Unmap()
	if Never(ip) {
		return ErrUnreachable
	}
	if p.Private(ip) {
		return nil
	}
	if !Public(ip) {
		return &NetworkError{Address: ip, Suggest: suggest(ip)}
	}
	if p.RequirePrivate {
		return ErrNeedsTLS
	}
	if p.Trusted == nil || !p.Trusted(host) {
		return hosts.NotTrusted(host)
	}
	return nil
}

// Resolve returns the addresses of host that the policy allows, and the
// refusal when any of them is refused: a host that answers with an address
// it may not reach is refused whole, so DNS cannot pick the destination.
func (p Policy) Resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if err = p.Allow(host, ip); err != nil {
			return nil, err
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%s could not be resolved", host)
	}
	for _, ip := range ips {
		if err = p.Allow(host, ip); err != nil {
			return nil, err
		}
	}
	return ips, nil
}

// DialContext connects to address (host:port) as the policy allows,
// dialling the checked addresses themselves. It ignores proxy settings,
// which could resolve the host somewhere else.
func (p Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid destination")
	}
	ips, err := p.Resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var last error
	for _, ip := range ips {
		c, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, fmt.Errorf("%s could not be reached: %w", host, last)
}

// ParseNetworks reads an operator's list of networks: CIDRs, or single
// addresses, separated by commas or spaces. A network that covers every
// address, or one no setting opens, is refused.
func ParseNetworks(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		prefix, err := netip.ParsePrefix(field)
		if err != nil {
			ip, e := netip.ParseAddr(field)
			if e != nil {
				return nil, fmt.Errorf("%q is not a network such as 192.168.1.0/24", field)
			}
			prefix = netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen())
		}
		prefix = prefix.Masked()
		if prefix.Bits() < 8 {
			return nil, fmt.Errorf("%s covers too many addresses; list the networks PaperGo should reach", prefix)
		}
		if Never(prefix.Addr()) {
			return nil, fmt.Errorf("%s is never reachable from PaperGo", prefix)
		}
		out = append(out, prefix)
	}
	return out, nil
}
