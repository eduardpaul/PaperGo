package egress

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"papergo/internal/hosts"
)

func TestPublicAndNever(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "::ffff:127.0.0.1", "::1", "169.254.169.254", "100.64.0.1", "fc00::1", "2002:a00:1::", "192.0.2.1", "172.18.1.3"} {
		if Public(netip.MustParseAddr(ip)) {
			t.Error("public", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !Public(netip.MustParseAddr(ip)) {
			t.Error("not public", ip)
		}
	}
	for _, ip := range []string{"169.254.169.254", "0.0.0.0", "224.0.0.1", "::", "fe80::1", "ff02::1", "fd00:ec2::254", "::ffff:169.254.169.254"} {
		if !Never(netip.MustParseAddr(ip)) {
			t.Error("reachable", ip)
		}
	}
	for _, ip := range []string{"192.168.1.20", "100.100.1.1", "10.0.0.1", "8.8.8.8", "fd12::1"} {
		if Never(netip.MustParseAddr(ip)) {
			t.Error("never", ip)
		}
	}
}

func TestPolicyAllow(t *testing.T) {
	nets, err := ParseNetworks("192.168.1.0/24, 100.64.0.0/10 10.0.0.5")
	if err != nil || len(nets) != 3 || nets[2] != netip.MustParsePrefix("10.0.0.5/32") {
		t.Fatal(nets, err)
	}
	trusted := func(host string) bool { return host == "broker.example.com" }
	p := Policy{Networks: nets, Trusted: trusted}
	cases := []struct {
		host, ip string
		want     error
	}{
		{"192.168.1.20", "192.168.1.20", nil},
		{"nas", "100.70.0.1", nil},
		{"pi", "10.0.0.5", nil},
		{"router", "10.0.0.6", &NetworkError{}},
		{"broker.example.com", "8.8.8.8", nil},
		{"other.example.com", "8.8.8.8", hosts.ErrNotTrusted},
		{"metadata", "169.254.169.254", ErrUnreachable},
	}
	for _, c := range cases {
		err := p.Allow(c.host, netip.MustParseAddr(c.ip))
		switch want := c.want.(type) {
		case nil:
			if err != nil {
				t.Errorf("%s %s: %v", c.host, c.ip, err)
			}
		case *NetworkError:
			var got *NetworkError
			if !errors.As(err, &got) || got.Suggest != netip.MustParsePrefix("10.0.0.0/24") {
				t.Errorf("%s %s: %v", c.host, c.ip, err)
			}
		default:
			if !errors.Is(err, want) {
				t.Errorf("%s %s: %v, want %v", c.host, c.ip, err, want)
			}
		}
	}
	if (&NetworkError{Address: netip.MustParseAddr("192.168.5.2"), Suggest: netip.MustParsePrefix("192.168.5.0/24")}).Error() != "192.168.5.2 is on a private network; ask the operator to add 192.168.5.0/24 to connections.networks" {
		t.Fatal("message")
	}
}

func TestParseNetworksRefusesTooWide(t *testing.T) {
	for _, raw := range []string{"0.0.0.0/0", "169.254.0.0/16", "nonsense", "::/0"} {
		if _, err := ParseNetworks(raw); err == nil {
			t.Error("accepted", raw)
		}
	}
	if n, err := ParseNetworks(""); err != nil || len(n) != 0 {
		t.Fatal(n, err)
	}
}

func TestDialChecksAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.Close()
		}
	}()
	refused := Policy{}
	if _, err = refused.DialContext(context.Background(), "tcp", ln.Addr().String()); err == nil {
		t.Fatal("dialled loopback outside the operator's networks")
	}
	allowed := Policy{Networks: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
	c, err := allowed.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	plain := Policy{Networks: allowed.Networks, Trusted: func(string) bool { return true }, RequirePrivate: true}
	if err = plain.Allow("127.0.0.1", netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	if err = plain.Allow("one.one.one.one", netip.MustParseAddr("1.1.1.1")); !errors.Is(err, ErrNeedsTLS) {
		t.Fatalf("a trusted public host without TLS: %v", err)
	}
}
