package portmap

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/huin/goupnp"
)

// ── LAN-only HTTP for UPnP ───────────────────────────────────────────────────
//
// UPnP discovery hands the server URLs chosen by whatever answered the SSDP
// multicast: the device description (LOCATION) and, inside it, the SOAP
// control URL. Any device on the LAN can answer. So the HTTP client goupnp
// uses only connects to private-network addresses (RFC 1918, IPv6 ULA,
// link-local, CGNAT 100.64/10) — never the internet, never this host's
// loopback (where the unauthenticated metrics/pprof port lives) — follows no
// redirects, times out quickly and reads at most 1 MiB of any response.

const (
	upnpHTTPTimeout = 5 * time.Second
	upnpMaxBody     = 1 << 20
)

var errNotLAN = errors.New("refusing a UPnP URL outside the local network")

// lanAddr reports whether a dial target is on the local network.
func lanAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() {
		return false
	}
	return a.IsPrivate() || a.IsLinkLocalUnicast() || cgnat.Contains(a)
}

// lanOnlyControl refuses a connection to anything but a LAN address. It runs
// after name resolution, on the address actually dialled.
func lanOnlyControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", errNotLAN, address)
	}
	if !lanAddr(ap.Addr()) {
		return fmt.Errorf("%w: %s", errNotLAN, address)
	}
	return nil
}

// limitedBody caps how much of a response goupnp can read.
type limitedBody struct {
	io.Reader
	io.Closer
}

type limitTransport struct{ base http.RoundTripper }

func (t limitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = limitedBody{Reader: io.LimitReader(resp.Body, upnpMaxBody), Closer: resp.Body}
	return resp, nil
}

// lanHTTPClient is the client for UPnP description and SOAP requests.
func lanHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: upnpHTTPTimeout, Control: lanOnlyControl}
	tr := &http.Transport{
		Proxy:                 nil, // never through an HTTP proxy: the router is local
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: upnpHTTPTimeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: limitTransport{base: tr},
		Timeout:   upnpHTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

var installLANClient sync.Once

// useLANHTTP points goupnp's description fetches at the LAN-only client
// (once per process; nothing else in OnScreen uses goupnp).
func useLANHTTP() {
	installLANClient.Do(func() { goupnp.HTTPClientDefault = lanHTTPClient() })
}

// lanSOAP gives a discovered service's SOAP calls the same client.
func lanSOAP(c igdClient) {
	if sc := c.GetServiceClient(); sc != nil && sc.SOAPClient != nil {
		sc.SOAPClient.HTTPClient = *lanHTTPClient()
	}
}
