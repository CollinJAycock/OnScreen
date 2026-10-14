package portmap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/jackpal/gateway"
	natpmp "github.com/jackpal/go-nat-pmp"
)

// ── NAT-PMP (RFC 6886) ───────────────────────────────────────────────────────

// natpmpGateway maps ports through a NAT-PMP server (github.com/jackpal/go-nat-pmp).
type natpmpGateway struct {
	gw    net.IP
	local net.IP
	c     *natpmp.Client
}

func newNATPMPGateway(gw, local net.IP) *natpmpGateway {
	return &natpmpGateway{gw: gw, local: local, c: natpmp.NewClientWithTimeout(gw, 4*time.Second)}
}

func (g *natpmpGateway) Method() string  { return MethodNATPMP }
func (g *natpmpGateway) Addr() string    { return g.gw.String() }
func (g *natpmpGateway) LocalIP() net.IP { return g.local }

func (g *natpmpGateway) ExternalIP(context.Context) (net.IP, error) {
	r, err := g.c.GetExternalAddress()
	if err != nil {
		return nil, err
	}
	return net.IP(r.ExternalIPAddress[:]), nil
}

func (g *natpmpGateway) AddMapping(_ context.Context, m Mapping) (Granted, error) {
	r, err := g.c.AddPortMappingWithDuration(strings.ToLower(m.Protocol), m.InternalPort, m.ExternalPort, m.Lease)
	if err != nil {
		return Granted{}, err
	}
	return Granted{
		ExternalPort: int(r.MappedExternalPort),
		Lease:        time.Duration(r.PortMappingLifetimeInSeconds) * time.Second,
	}, nil
}

// DeleteMapping: a request with lifetime 0 and external port 0 (RFC 6886 §3.4).
func (g *natpmpGateway) DeleteMapping(_ context.Context, m Mapping) error {
	_, err := g.c.AddPortMapping(strings.ToLower(m.Protocol), m.InternalPort, 0, 0)
	return err
}

// ── Discovery ────────────────────────────────────────────────────────────────

// LANDiscoverer finds the router: UPnP IGD over SSDP first (what most home
// routers speak), then PCP and NAT-PMP at the default gateway.
type LANDiscoverer struct {
	// Timeout bounds the whole discovery. Default 6 s.
	Timeout time.Duration
	Logger  *slog.Logger
}

// Discover implements Discoverer.
func (d LANDiscoverer) Discover(ctx context.Context) (Gateway, error) {
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gw, upnpErr := discoverUPnP(ctx)
	if upnpErr == nil {
		return gw, nil
	}
	logger.DebugContext(ctx, "remote access: no UPnP gateway", "err", upnpErr)

	gwIP, err := gateway.DiscoverGateway()
	if err != nil || gwIP == nil || gwIP.To4() == nil {
		return nil, fmt.Errorf("%w (UPnP: %v; default gateway: %v)", ErrNoGateway, upnpErr, err)
	}
	// PCP / NAT-PMP talk to the default gateway; on a host whose gateway
	// isn't a home router on a private network (a VPS, a container on a
	// routed network) there is nothing to ask.
	if a, ok := netip.AddrFromSlice(gwIP.To4()); !ok || !lanAddr(a) {
		return nil, fmt.Errorf("%w (UPnP: %v; the default gateway %s is not on a private network)", ErrNoGateway, upnpErr, gwIP)
	}
	local, err := localIPToward(gwIP)
	if err != nil {
		return nil, fmt.Errorf("local address toward %s: %w", gwIP, err)
	}

	// PCP first. The probe is a delete (lifetime 0) of a mapping that
	// doesn't exist — no side effect — and any PCP answer, even a refusal,
	// shows the server speaks it. A NAT-PMP-only server rejects version 2.
	pcp := newPCPGateway(gwIP, local)
	pcp.timeout = 2 * time.Second
	_, err = pcp.mapRequest(ctx, local, 9, 0, 0)
	var refused *pcpResultError
	if err == nil || errors.As(err, &refused) {
		pcp.timeout = 4 * time.Second
		return pcp, nil
	}
	if !errors.Is(err, errPCPUnsupported) {
		logger.DebugContext(ctx, "remote access: no PCP answer", "gateway", gwIP, "err", err)
	}

	pmp := newNATPMPGateway(gwIP, local)
	if _, err := pmp.ExternalIP(ctx); err != nil {
		return nil, fmt.Errorf("%w (UPnP: %v; NAT-PMP at %s: %v)", ErrNoGateway, upnpErr, gwIP, err)
	}
	return pmp, nil
}

// localIPToward is this host's address on the route to ip (no packet is
// sent: connecting a UDP socket only picks the route).
func localIPToward(ip net.IP) (net.IP, error) {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: ip, Port: pcpPort})
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return nil, errors.New("no local address")
	}
	return addr.IP.To4(), nil
}
