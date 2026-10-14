package portmap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// ── PCP (RFC 6887) ───────────────────────────────────────────────────────────
//
// A minimal Port Control Protocol client: the MAP opcode only, IPv4, over UDP
// to the default gateway's port 5351 (the port NAT-PMP uses too; PCP is its
// successor). No library implements just this in pure Go without pulling in
// much more, and the wire format is small.

const (
	pcpPort     = 5351
	pcpVersion  = 2
	pcpOpMap    = 1
	pcpProtoTCP = 6
	// pcpHeaderLen + pcpMapLen = 60: the request and response size.
	pcpHeaderLen = 24
	pcpMapLen    = 36
)

// PCP result codes (RFC 6887 §7.4) the client tells apart.
const (
	pcpSuccess             = 0
	pcpUnsuppVersion       = 1
	pcpNotAuthorized       = 2
	pcpNoResources         = 8
	pcpUnsuppProtocol      = 9
	pcpCannotProvideExtern = 11
)

// errPCPUnsupported: the gateway answered, but speaks NAT-PMP rather than
// PCP (it rejected version 2) — fall back to NAT-PMP.
var errPCPUnsupported = errors.New("gateway does not speak PCP")

// pcpResultError is a PCP server's refusal: it speaks PCP, but said no.
type pcpResultError struct{ code byte }

func (e *pcpResultError) Error() string {
	switch e.code {
	case pcpNotAuthorized:
		return "the router refused the mapping (PCP NOT_AUTHORIZED)"
	case pcpNoResources:
		return "the router has no room for another mapping (PCP NO_RESOURCES)"
	case pcpUnsuppProtocol:
		return "the router can't map TCP (PCP UNSUPP_PROTOCOL)"
	case pcpCannotProvideExtern:
		return "the router can't provide that external port (PCP CANNOT_PROVIDE_EXTERNAL)"
	}
	return fmt.Sprintf("the router refused the mapping (PCP result %d)", e.code)
}

// errPCPOtherNonce: a response for some other client's mapping.
var errPCPOtherNonce = errors.New("PCP response for another mapping")

// pcpGateway maps ports through a PCP server.
type pcpGateway struct {
	gateway net.IP
	port    int // 5351; tests point it elsewhere
	local   net.IP
	timeout time.Duration

	mu sync.Mutex
	// nonce identifies this client's mapping to the server: a renewal or
	// delete must repeat it (RFC 6887 §11.1).
	nonce    [12]byte
	external net.IP // the assigned external address, from the last MAP
}

func newPCPGateway(gateway, local net.IP) *pcpGateway {
	g := &pcpGateway{gateway: gateway.To4(), port: pcpPort, local: local, timeout: 4 * time.Second}
	_, _ = rand.Read(g.nonce[:])
	return g
}

func (g *pcpGateway) Method() string  { return MethodPCP }
func (g *pcpGateway) Addr() string    { return g.gateway.String() }
func (g *pcpGateway) LocalIP() net.IP { return g.local }

// ExternalIP is the address the last MAP response assigned (PCP has no
// separate "get external address" request).
func (g *pcpGateway) ExternalIP(context.Context) (net.IP, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.external == nil {
		return nil, errors.New("no mapping yet")
	}
	return g.external, nil
}

func (g *pcpGateway) AddMapping(ctx context.Context, m Mapping) (Granted, error) {
	lease := uint32(m.Lease / time.Second)
	if lease == 0 {
		lease = 3600
	}
	resp, err := g.mapRequest(ctx, m.InternalIP, m.InternalPort, m.ExternalPort, lease)
	if err != nil {
		return Granted{}, err
	}
	g.mu.Lock()
	g.external = resp.externalIP
	g.mu.Unlock()
	return Granted{ExternalPort: resp.externalPort, Lease: time.Duration(resp.lifetime) * time.Second}, nil
}

// DeleteMapping is a MAP with lifetime 0 and the same nonce.
func (g *pcpGateway) DeleteMapping(ctx context.Context, m Mapping) error {
	_, err := g.mapRequest(ctx, m.InternalIP, m.InternalPort, m.ExternalPort, 0)
	return err
}

type pcpMapResponse struct {
	lifetime     uint32
	externalPort int
	externalIP   net.IP
}

// encodePCPMap builds a 60-byte MAP request.
func encodePCPMap(client net.IP, nonce [12]byte, internalPort, suggestedPort int, lifetime uint32) []byte {
	b := make([]byte, pcpHeaderLen+pcpMapLen)
	b[0] = pcpVersion
	b[1] = pcpOpMap // R bit 0: a request
	binary.BigEndian.PutUint32(b[4:8], lifetime)
	copy(b[8:24], client.To16()) // IPv4-mapped IPv6 (::ffff:a.b.c.d)
	o := b[pcpHeaderLen:]
	copy(o[0:12], nonce[:])
	o[12] = pcpProtoTCP
	binary.BigEndian.PutUint16(o[16:18], uint16(internalPort))
	binary.BigEndian.PutUint16(o[18:20], uint16(suggestedPort))
	// Suggested external address: "any IPv4" is ::ffff:0.0.0.0.
	copy(o[20:36], net.IPv4zero.To16())
	return b
}

// decodePCPMap checks a MAP response against the request's nonce.
func decodePCPMap(b []byte, nonce [12]byte) (pcpMapResponse, error) {
	if len(b) >= 2 && b[0] == 0 {
		// A NAT-PMP server's reply to an unknown version (RFC 6886 §3.5).
		return pcpMapResponse{}, errPCPUnsupported
	}
	if len(b) < pcpHeaderLen+pcpMapLen {
		return pcpMapResponse{}, fmt.Errorf("short PCP response (%d bytes)", len(b))
	}
	if b[0] != pcpVersion || b[1] != 0x80|pcpOpMap {
		return pcpMapResponse{}, fmt.Errorf("unexpected PCP response (version %d, opcode %#x)", b[0], b[1])
	}
	if code := b[3]; code != pcpSuccess {
		if code == pcpUnsuppVersion {
			return pcpMapResponse{}, errPCPUnsupported
		}
		return pcpMapResponse{}, &pcpResultError{code: code}
	}
	o := b[pcpHeaderLen:]
	if [12]byte(o[0:12]) != nonce {
		return pcpMapResponse{}, errPCPOtherNonce
	}
	return pcpMapResponse{
		lifetime:     binary.BigEndian.Uint32(b[4:8]),
		externalPort: int(binary.BigEndian.Uint16(o[18:20])),
		externalIP:   net.IP(append([]byte(nil), o[20:36]...)).To4(),
	}, nil
}

// mapRequest sends a MAP and waits for the matching answer, retrying with a
// doubling timeout (1, 2, 4 s… within g.timeout overall) as RFC 6887 §8.1.1
// asks.
func (g *pcpGateway) mapRequest(ctx context.Context, internalIP net.IP, internalPort, externalPort int, lifetime uint32) (pcpMapResponse, error) {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: g.gateway, Port: g.port})
	if err != nil {
		return pcpMapResponse{}, err
	}
	defer func() { _ = conn.Close() }()
	g.mu.Lock()
	nonce := g.nonce
	g.mu.Unlock()
	req := encodePCPMap(internalIP, nonce, internalPort, externalPort, lifetime)

	deadline := time.Now().Add(g.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	wait := time.Second
	buf := make([]byte, 1100)
	for {
		if _, err := conn.Write(req); err != nil {
			return pcpMapResponse{}, err
		}
		next := time.Now().Add(wait)
		if next.After(deadline) {
			next = deadline
		}
		_ = conn.SetReadDeadline(next)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // resend
				}
				return pcpMapResponse{}, err
			}
			resp, err := decodePCPMap(buf[:n], nonce)
			if errors.Is(err, errPCPOtherNonce) {
				continue // a stray answer: keep listening
			}
			return resp, err
		}
		if ctx.Err() != nil {
			return pcpMapResponse{}, ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return pcpMapResponse{}, errors.New("no answer from the router over PCP")
		}
		wait *= 2
	}
}
