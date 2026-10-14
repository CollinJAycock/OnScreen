package portmap

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// ExternalAddressPrivate reports whether the router's "external" address is
// itself private — carrier-grade NAT (100.64.0.0/10) or a second router in
// front (RFC 1918): a mapping on this router then doesn't reach the
// internet.
func ExternalAddressPrivate(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsPrivate() || cgnat.Contains(a) || a.IsLoopback() || a.IsLinkLocalUnicast()
}

// ReachabilityHint is the status page's advice. The server doesn't test its
// own reachability from the internet (that would need an outside service to
// call back); it tells the admin what to try instead.
func ReachabilityHint(st Status, https bool) string {
	switch st.State {
	case StateDisabled:
		return "Automatic port mapping is off. To reach the server from outside your network, forward a port on your router by hand or use a VPN such as Tailscale."
	case StateLoopbackOnly:
		return "The server listens on loopback only, so it can't be reached from other devices. Change the listen address (Settings ▸ Nodes) before enabling remote access."
	case StateNoGateway:
		return "No router answered over UPnP, PCP or NAT-PMP. Turn on UPnP or NAT-PMP in your router's settings, or forward the port by hand. In Docker the server needs host networking for discovery to reach the router."
	case StateStandby:
		return "Waiting for the node that manages the mapping to report."
	case StateMapped:
	default:
		if st.LastError != "" {
			return "The router didn't accept the mapping: " + st.LastError + ". Check that UPnP / NAT-PMP is enabled and the external port isn't used by another device, or forward the port by hand."
		}
		return ""
	}
	if st.ExternalIP == "" {
		return fmt.Sprintf("Port %d is mapped, but the router didn't report its public address. Test from a device outside your network (for example a phone on mobile data).", st.ExternalPort)
	}
	if ExternalAddressPrivate(st.ExternalIP) {
		return fmt.Sprintf("The router's outside address (%s) is itself private — your provider uses carrier-grade NAT, or there is a second router in front. The mapping won't make the server reachable from the internet; use a VPN such as Tailscale, or ask your provider for a public IP.", st.ExternalIP)
	}
	scheme := "http"
	if https {
		scheme = "https"
	}
	url := scheme + "://" + net.JoinHostPort(st.ExternalIP, strconv.Itoa(st.ExternalPort))
	return "Try " + url + " from a device outside your network (for example a phone on mobile data, Wi-Fi off). Many routers can't loop back to their own public address, so a test from inside your network may fail even when the mapping works."
}
