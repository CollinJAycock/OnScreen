package portmap

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestLANAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.1":     true,
		"10.0.0.138":      true,
		"172.16.0.1":      true,
		"100.64.0.1":      true,
		"169.254.1.1":     true,
		"fd00::1":         true,
		"fe80::1":         true,
		"127.0.0.1":       false, // this host: the metrics / pprof port lives here
		"::1":             false,
		"0.0.0.0":         false,
		"8.8.8.8":         false,
		"203.0.113.7":     false,
		"239.255.255.250": false,
	} {
		if got := lanAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("lanAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

// A device answering SSDP can name any URL; the UPnP client must not fetch
// one on this host's loopback (or anywhere off the LAN).
func TestLANHTTPClient_RefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()
	_, err := lanHTTPClient().Get(srv.URL)
	if err == nil || !errors.Is(err, errNotLAN) {
		t.Fatalf("fetching %s: err = %v, want errNotLAN", srv.URL, err)
	}
}
