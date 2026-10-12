package auth

import (
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"papergo/internal/identity"
	"strings"
	"unicode"
)

const ProxySubjectHeader = "X-Papergo-Subject"
const ProxySecretHeader = "X-Papergo-Proxy-Secret"

// Proxy accepts an identity asserted by an authenticated, explicitly trusted peer.
// It never trusts forwarded client addresses or interprets client credentials.
type Proxy struct {
	secret string
	peers  []netip.Prefix
	origin string
}

func NewProxy(secret string, peers []netip.Prefix, origin string) (*Proxy, error) {
	if len(secret) < 32 || len(secret) > 256 || strings.ContainsAny(secret, "\r\n\x00") || len(peers) == 0 {
		return nil, errors.New("proxy authentication needs a strong secret and trusted peers")
	}
	for _, peer := range peers {
		if !peer.IsValid() || peer.Bits() == 0 {
			return nil, errors.New("proxy trust requires bounded, valid networks")
		}
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("proxy authentication needs an HTTPS public origin")
	}
	return &Proxy{secret: secret, peers: append([]netip.Prefix(nil), peers...), origin: canonicalProxyOrigin(u)}, nil
}
func (*Proxy) Kind() Kind { return TrustedProxy }
func (p *Proxy) Authenticate(r *http.Request) (Identity, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return Identity{}, ErrUnauthenticated
	}
	trusted := false
	for _, prefix := range p.peers {
		trusted = trusted || prefix.Contains(peer.Unmap())
	}
	if !trusted || len(r.Header.Values(ProxySecretHeader)) != 1 || len(r.Header.Values(ProxySubjectHeader)) != 1 || r.Header.Get("Authorization") != "" {
		return Identity{}, ErrUnauthenticated
	}
	if subtle.ConstantTimeCompare([]byte(p.secret), []byte(r.Header.Get(ProxySecretHeader))) != 1 {
		return Identity{}, ErrUnauthenticated
	}
	subject := r.Header.Get(ProxySubjectHeader)
	if identity.ValidateSubject(subject) != nil || strings.IndexFunc(subject, unicode.IsControl) >= 0 {
		return Identity{}, ErrUnauthenticated
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			return Identity{}, ErrUnauthenticated
		}
		if origins := r.Header.Values("Origin"); len(origins) > 0 {
			if len(origins) != 1 || !p.sameOrigin(origins[0], false) {
				return Identity{}, ErrUnauthenticated
			}
		} else if referer := r.Header.Get("Referer"); referer != "" {
			if !p.sameOrigin(referer, true) {
				return Identity{}, ErrUnauthenticated
			}
		}
	}
	return Identity{Subject: subject}, nil
}

func canonicalProxyOrigin(u *url.URL) string {
	return "https://" + strings.ToLower(strings.TrimSuffix(u.Host, ":443"))
}
func (p *Proxy) sameOrigin(raw string, referer bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	if !referer && (u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "") {
		return false
	}
	return canonicalProxyOrigin(u) == p.origin
}
