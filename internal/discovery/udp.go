// Package discovery implements LAN auto-discovery so native clients (TV apps,
// mobile apps, set-top boxes) can find an OnScreen server without the user
// typing in an address.
//
// The protocol is intentionally tiny: a client UDP-broadcasts a probe string
// to the well-known port and the server replies with a JSON document
// describing itself. This sidesteps the complexity of full mDNS while still
// working in the environments where it matters (a single broadcast domain).
//
// For Docker deployments the container must use host networking
// (--network=host) so the listener sees the broadcast packets.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// DefaultPort is the UDP port the discovery listener binds to. Documented in
// the OSS README so third-party native clients know where to probe.
const DefaultPort = 7368

// ProbePrefix is the case-insensitive request prefix a client sends to find
// OnScreen servers. Anything else is ignored — keeps the listener silent on
// the noisy LAN broadcast channel.
const ProbePrefix = "OnScreen-Discovery-Probe"

// ServerInfo is the payload returned to discovery probes. Keep this small —
// it travels in a single UDP packet.
type ServerInfo struct {
	ServerName string `json:"server_name"`
	MachineID  string `json:"machine_id"`
	Version    string `json:"version"`
	HTTPURL    string `json:"http_url,omitempty"`
	HTTPSURL   string `json:"https_url,omitempty"`
}

// Listener is the UDP discovery server.
type Listener struct {
	port   int
	info   func() ServerInfo
	logger *slog.Logger
}

// NewListener constructs a listener bound to port. The info callback runs on
// each probe — keep it cheap (no DB calls) since broadcasts can be frequent.
func NewListener(port int, info func() ServerInfo, logger *slog.Logger) *Listener {
	if port <= 0 {
		port = DefaultPort
	}
	return &Listener{port: port, info: info, logger: logger}
}

// Run blocks until ctx is cancelled. Returns the first non-cancellation error
// from the underlying socket.
func (l *Listener) Run(ctx context.Context) error {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: l.port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("discovery: listen udp4 :%d: %w", l.port, err)
	}
	defer conn.Close()

	l.logger.Info("discovery listener started", "port", l.port)

	// Close the socket on context cancel so the blocking ReadFromUDP returns.
	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	buf := make([]byte, 1024)
	for {
		if ctx.Err() != nil {
			return nil
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			l.logger.Warn("discovery: read udp", "err", err)
			continue
		}
		msg := strings.TrimSpace(string(buf[:n]))
		if !strings.HasPrefix(strings.ToLower(msg), strings.ToLower(ProbePrefix)) {
			continue
		}
		l.respond(conn, src)
	}
}

// respond writes the server info JSON back to src. Failures are logged but
// not fatal — a lost discovery reply just means the client keeps probing.
func (l *Listener) respond(conn *net.UDPConn, src *net.UDPAddr) {
	body, err := json.Marshal(l.info())
	if err != nil {
		l.logger.Warn("discovery: marshal info", "err", err)
		return
	}
	if _, err := conn.WriteToUDP(body, src); err != nil {
		l.logger.Warn("discovery: write reply", "remote", src.String(), "err", err)
	}
}
