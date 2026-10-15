package portmap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/huin/goupnp"
	"github.com/huin/goupnp/dcps/internetgateway2"
)

// igdClient is the part of a WANIPConnection / WANPPPConnection service
// client the mapper uses — the same methods on all three service types.
type igdClient interface {
	AddPortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string,
		internalPort uint16, internalClient string, enabled bool, description string, leaseDuration uint32) error
	DeletePortMappingCtx(ctx context.Context, remoteHost string, externalPort uint16, protocol string) error
	GetExternalIPAddressCtx(ctx context.Context) (string, error)
	GetServiceClient() *goupnp.ServiceClient
}

// upnpGateway maps ports through a UPnP Internet Gateway Device.
type upnpGateway struct {
	c       igdClient
	service string // e.g. "WANIPConnection:2", for the status line
	// permanentOnly is set once the router answered error 725
	// (OnlyPermanentLeasesSupported): later requests ask for lease 0.
	mu            sync.Mutex
	permanentOnly bool
}

func (g *upnpGateway) Method() string { return MethodUPnP }

func (g *upnpGateway) Addr() string {
	sc := g.c.GetServiceClient()
	if sc != nil && sc.Location != nil {
		return sc.Location.Hostname() + " (" + g.service + ")"
	}
	return g.service
}

func (g *upnpGateway) LocalIP() net.IP {
	if sc := g.c.GetServiceClient(); sc != nil {
		return sc.LocalAddr()
	}
	return nil
}

func (g *upnpGateway) ExternalIP(ctx context.Context) (net.IP, error) {
	s, err := g.c.GetExternalIPAddressCtx(ctx)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil, fmt.Errorf("router reported external address %q", s)
	}
	return ip, nil
}

// errOnlyPermanent recognises UPnP error 725, OnlyPermanentLeasesSupported:
// an IGDv1 router that can't time a lease out.
func errOnlyPermanent(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "725") || strings.Contains(msg, "OnlyPermanentLeasesSupported")
}

func (g *upnpGateway) AddMapping(ctx context.Context, m Mapping) (Granted, error) {
	g.mu.Lock()
	permanent := g.permanentOnly
	g.mu.Unlock()
	lease := uint32(m.Lease / time.Second)
	if permanent {
		lease = 0
	}
	add := func(lease uint32) error {
		return g.c.AddPortMappingCtx(ctx, "", uint16(m.ExternalPort), m.Protocol,
			uint16(m.InternalPort), m.InternalIP.String(), true, m.Description, lease)
	}
	err := add(lease)
	if err != nil && lease != 0 && errOnlyPermanent(err) {
		g.mu.Lock()
		g.permanentOnly = true
		g.mu.Unlock()
		lease = 0
		err = add(0)
	}
	if err != nil {
		return Granted{}, err
	}
	return Granted{ExternalPort: m.ExternalPort, Lease: time.Duration(lease) * time.Second}, nil
}

func (g *upnpGateway) DeleteMapping(ctx context.Context, m Mapping) error {
	return g.c.DeletePortMappingCtx(ctx, "", uint16(m.ExternalPort), m.Protocol)
}

// discoverUPnP looks for an IGD over SSDP (multicast on the LAN), preferring
// WANIPConnection:2, then WANIPConnection:1, then WANPPPConnection:1 — the
// service types of IGD v2 and v1 routers. Every HTTP request it and the
// chosen service make goes through the LAN-only client (lanhttp.go).
func discoverUPnP(ctx context.Context) (Gateway, error) {
	type found struct {
		rank int
		gw   *upnpGateway
	}
	useLANHTTP()
	var (
		mu   sync.Mutex
		best *found
		wg   sync.WaitGroup
	)
	consider := func(rank int, service string, c igdClient) {
		mu.Lock()
		defer mu.Unlock()
		if best == nil || rank < best.rank {
			best = &found{rank: rank, gw: &upnpGateway{c: c, service: service}}
		}
	}
	wg.Add(3)
	go func() {
		defer wg.Done()
		if cs, _, err := internetgateway2.NewWANIPConnection2ClientsCtx(ctx); err == nil && len(cs) > 0 {
			consider(0, "WANIPConnection:2", cs[0])
		}
	}()
	go func() {
		defer wg.Done()
		if cs, _, err := internetgateway2.NewWANIPConnection1ClientsCtx(ctx); err == nil && len(cs) > 0 {
			consider(1, "WANIPConnection:1", cs[0])
		}
	}()
	go func() {
		defer wg.Done()
		if cs, _, err := internetgateway2.NewWANPPPConnection1ClientsCtx(ctx); err == nil && len(cs) > 0 {
			consider(2, "WANPPPConnection:1", cs[0])
		}
	}()
	wg.Wait()
	if best == nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("no UPnP Internet Gateway Device answered")
	}
	lanSOAP(best.gw.c)
	return best.gw, nil
}
