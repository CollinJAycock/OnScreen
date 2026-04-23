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

func TestCheckAddress_AllowsPublic(t *testing.T) {
	for _, a := range []string{"1.1.1.1:443", "8.8.8.8:53", "[2606:4700::1111]:443"} {
		if err := checkAddress(DialPolicy{}, a); err != nil {
			t.Errorf("%s: expected allowed, got %v", a, err)
		}
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
