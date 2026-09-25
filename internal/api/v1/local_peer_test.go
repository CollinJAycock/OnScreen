package v1

import (
	"net"
	"net/http/httptest"
	"testing"
)

// The first-run gate's notion of "local": private ranges plus Tailscale/CGNAT
// and this host's own IPv6 /64s (dual-stack LANs hand out global IPv6). Public
// IPv4 — even a neighbour in the host's own public subnet — stays remote.
func TestIsLocalPeer(t *testing.T) {
	peer := func(addr string) bool {
		r := httptest.NewRequest("POST", "/api/v1/auth/register", nil)
		r.RemoteAddr = addr
		return isLocalPeer(r)
	}
	for _, a := range []string{"127.0.0.1:1", "192.168.1.5:1", "10.0.0.2:1", "[fd00::5]:1", "[fe80::1]:1", "100.101.102.103:1"} {
		if !peer(a) {
			t.Errorf("%s should be local", a)
		}
	}
	for _, a := range []string{"203.0.113.7:1", "8.8.8.8:1", "100.128.0.1:1", "[2001:db8:dead::1]:1"} {
		if peer(a) {
			t.Errorf("%s should NOT be local", a)
		}
	}
	// An IPv6 address inside one of this machine's own global /64s is local.
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() != nil || !n.IP.IsGlobalUnicast() || n.IP.IsPrivate() || ifc.Flags&net.FlagLoopback != 0 {
				continue
			}
			if ones, _ := n.Mask.Size(); ones < 64 {
				continue
			}
			sibling := make(net.IP, len(n.IP))
			copy(sibling, n.IP)
			sibling[15] ^= 0x01
			if !peer("[" + sibling.String() + "]:1") {
				t.Errorf("on-link IPv6 %s (prefix %s) should be local", sibling, n)
			}
			return
		}
	}
}
