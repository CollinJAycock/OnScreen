package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckAddress_BlocksPrivate(t *testing.T) {
	cases := []string{
		"10.0.0.1:80",
		"192.168.1.1:443",
		"172.16.0.5:22",
	}
	for _, a := range cases {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
}

func TestCheckAddress_BlocksLoopback(t *testing.T) {
	for _, a := range []string{"127.0.0.1:80", "[::1]:80"} {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
}

func TestCheckAddress_BlocksLinkLocalIncludingMetadata(t *testing.T) {
	// 169.254.169.254 is the AWS/GCP/Azure instance-metadata endpoint.
	// SSRF-to-metadata is the #1 thing this guard exists to prevent.
	for _, a := range []string{"169.254.169.254:80", "169.254.1.1:443"} {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
}

func TestCheckAddress_BlocksUnspecifiedAndMulticast(t *testing.T) {
	for _, a := range []string{"0.0.0.0:80", "[::]:80", "224.0.0.1:80"} {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
}

func TestCheckAddress_BlocksNAT64And6to4EmbeddingPrivate(t *testing.T) {
	// NAT64 (64:ff9b::/96) and 6to4 (2002::/16) tunnel an IPv4 address inside
	// IPv6; a gateway forwards them to the embedded v4. An embedded
	// private/metadata IP must be blocked (SSRF-to-metadata via NAT64).
	for _, a := range []string{
		"[64:ff9b::a9fe:a9fe]:80", // NAT64 → 169.254.169.254 (cloud metadata)
		"[64:ff9b::a00:1]:80",     // NAT64 → 10.0.0.1 (RFC1918)
		"[2002:a00:1::1]:80",      // 6to4 → embeds 10.0.0.1
		"[2002:7f00:1::1]:80",     // 6to4 → embeds 127.0.0.1 (loopback)
	} {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
	// A NAT64 address embedding a PUBLIC v4 (8.8.8.8) stays allowed.
	if err := checkAddress(DialPolicy{}, "[64:ff9b::808:808]:80"); err != nil {
		t.Errorf("NAT64 public-embedded: expected allowed, got %v", err)
	}
}

func TestCheckAddress_AllowsPublic(t *testing.T) {
	// 100.128.0.1 is just outside RFC 6598 (100.64.0.0/10 ends at
	// 100.127.255.255), so it must remain a normal public address.
	for _, a := range []string{"1.1.1.1:443", "8.8.8.8:53", "[2606:4700::1111]:443", "100.128.0.1:80"} {
		if err := checkAddress(DialPolicy{}, a); err != nil {
			t.Errorf("%s: expected allowed, got %v", a, err)
		}
	}
}

func TestCheckAddress_BlocksCGNAT(t *testing.T) {
	// RFC 6598 shared space (100.64.0.0/10) isn't covered by IsPrivate; it
	// must be blocked under the public-only policy so it can't be used to
	// reach a CGNAT/Tailscale-internal host.
	for _, a := range []string{"100.64.0.1:80", "100.127.255.254:443"} {
		if err := checkAddress(DialPolicy{}, a); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: expected ErrBlockedAddress, got %v", a, err)
		}
	}
}

func TestCheckAddress_AllowsCGNATWhenPrivateAllowed(t *testing.T) {
	// AllowPrivate opts into shared/CGNAT space too (Tailscale / CGNAT LANs).
	if err := checkAddress(DialPolicy{AllowPrivate: true}, "100.64.0.1:80"); err != nil {
		t.Errorf("AllowPrivate should permit CGNAT; got %v", err)
	}
}

func TestCheckAddress_LocalDevicePolicyAllowsPrivate(t *testing.T) {
	p := DialPolicy{AllowPrivate: true, AllowLoopback: true, AllowLinkLocal: true}
	for _, a := range []string{"10.0.0.1:80", "127.0.0.1:80", "169.254.1.1:80"} {
		if err := checkAddress(p, a); err != nil {
			t.Errorf("LocalDevice policy should allow %s; got %v", a, err)
		}
	}
}

func TestCheckAddress_UnresolvedHostBlocked(t *testing.T) {
	// A hostname (not an IP) in the address means DNS hasn't run yet.
	// The Control callback fires AFTER resolution in real usage, so
	// seeing a non-IP here is an anomaly worth blocking.
	if err := checkAddress(DialPolicy{}, "example.com:80"); !errors.Is(err, ErrBlockedAddress) {
		t.Error("expected unresolved host to be blocked")
	}
}

// End-to-end: a client constructed via Default() cannot reach a
// httptest server (which always binds to 127.0.0.1). Proves the
// Control hook is wired correctly through the http.Client → Transport
// → DialContext chain.
func TestDefault_BlocksLoopbackHTTPServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := Default()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected blocked dial to public httptest loopback")
	}
	// Error comes wrapped in net.OpError → we check the message contains
	// the reason. errors.Is through net.OpError's Unwrap chain works in
	// Go 1.20+; fall back to string match for robustness.
	if !errors.Is(err, ErrBlockedAddress) && !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected ErrBlockedAddress, got %v", err)
	}
}

func TestLocalDevice_AllowsLoopbackHTTPServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := LocalDevice()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("LocalDevice should allow loopback: %v", err)
	}
	resp.Body.Close()
}

func TestNewClient_TimeoutRespected(t *testing.T) {
	c := NewClient(DialPolicy{}, 1*time.Millisecond)
	if c.Timeout != 1*time.Millisecond {
		t.Errorf("timeout not propagated: %v", c.Timeout)
	}
}
