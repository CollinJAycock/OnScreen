package portmap

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ParseListenAddr splits a listen address (":7070", "0.0.0.0:7070",
// "192.168.1.5:8443", "[::]:7070") into host and port. An empty host means
// every interface.
func ParseListenAddr(addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", 0, fmt.Errorf("listen address %q: %w", addr, err)
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		return "", 0, fmt.Errorf("listen address %q: bad port", addr)
	}
	return h, n, nil
}

// IsLoopbackHost reports whether a listen host binds loopback only
// (127.0.0.0/8, ::1, "localhost"). Such a server is unreachable from the LAN,
// let alone the internet, and is never mapped.
func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
