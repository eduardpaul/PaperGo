package calendar

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const MaxBytes = 10 << 20
const MaxComponents = 2000

// Fetcher never puts the subscription URL or provider response in an error.
// DNS is checked at connection time and the checked address is dialled directly.
// No environment proxy is used: it could resolve the hostname independently.
type Fetcher struct{ client *http.Client }
type Download struct {
	Body               []byte
	ETag, LastModified string
	NotModified        bool
}

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

func PublicIP(ip netip.Addr) bool {
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
func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || len(raw) > 8192 || (u.Port() != "" && u.Port() != "443") {
		return nil, errors.New("use an HTTPS iCalendar URL on port 443 without credentials or a fragment")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !PublicIP(ip) {
		return nil, errors.New("calendar destinations must use public internet addresses")
	}
	u.Host = strings.ToLower(u.Host)
	if u.Port() == "443" {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}
func resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("calendar destination could not be resolved")
	}
	for _, ip := range ips {
		if !PublicIP(ip) {
			return nil, errors.New("calendar destinations must use public internet addresses")
		}
	}
	return ips, nil
}
func ValidateURL(ctx context.Context, raw string) (string, error) {
	u, err := ParseURL(raw)
	if err != nil {
		return "", err
	}
	if _, err = resolve(ctx, u.Hostname()); err != nil {
		return "", err
	}
	return u.String(), nil
}
func NewFetcher() *Fetcher {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid calendar destination")
			}
			ips, err := resolve(ctx, host)
			if err != nil {
				return nil, err
			}
			dialer := net.Dialer{Timeout: 10 * time.Second}
			for _, ip := range ips {
				c, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
			}
			return nil, errors.New("calendar destination could not be reached")
		}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10, DisableKeepAlives: true,
	}
	return &Fetcher{client: &http.Client{Transport: tr, Timeout: 45 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many calendar redirects")
		}
		if _, err := ParseURL(req.URL.String()); err != nil {
			return err
		}
		// Conditional validators belong to the original origin only.
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			req.Header.Del("If-None-Match")
			req.Header.Del("If-Modified-Since")
		}
		return nil
	}}}
}
func (f *Fetcher) Fetch(ctx context.Context, raw, etag, modified string) (Download, error) {
	var out Download
	u, err := ParseURL(raw)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return out, errors.New("invalid calendar request")
	}
	req.Header.Set("Accept", "text/calendar")
	req.Header.Set("User-Agent", "PaperGo/ical")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
	res, err := f.client.Do(req)
	if err != nil {
		return out, errors.New("calendar download failed; check the URL and public HTTPS access")
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified && (etag != "" || modified != "") {
		out.NotModified = true
		return out, nil
	}
	if res.StatusCode != http.StatusOK {
		return out, errors.New("calendar server did not return a complete feed")
	}
	if res.ContentLength > MaxBytes {
		return out, errors.New("calendar feed exceeds 10 MiB")
	}
	out.Body, err = io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	if err != nil {
		return Download{}, errors.New("calendar download was interrupted")
	}
	if len(out.Body) > MaxBytes {
		return Download{}, errors.New("calendar feed exceeds 10 MiB")
	}
	out.ETag, out.LastModified = res.Header.Get("ETag"), res.Header.Get("Last-Modified")
	return out, nil
}
