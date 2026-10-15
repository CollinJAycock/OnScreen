package portmap

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

// fakePCPServer answers MAP requests on a loopback UDP port. reply builds
// the answer to each request (nil = stay silent).
func fakePCPServer(t *testing.T, reply func(req []byte) []byte) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 1100)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if out := reply(append([]byte(nil), buf[:n]...)); out != nil {
				_, _ = conn.WriteToUDP(out, from)
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// pcpAnswer is a well-formed MAP response to req.
func pcpAnswer(req []byte, result byte, lifetime uint32, extPort uint16, extIP net.IP) []byte {
	b := make([]byte, 60)
	b[0] = 2
	b[1] = 0x80 | 1
	b[3] = result
	binary.BigEndian.PutUint32(b[4:8], lifetime)
	copy(b[24:36], req[24:36]) // nonce
	b[36] = req[36]            // protocol
	copy(b[40:42], req[40:42]) // internal port
	binary.BigEndian.PutUint16(b[42:44], extPort)
	copy(b[44:60], extIP.To16())
	return b
}

func testPCP(port int) *pcpGateway {
	g := newPCPGateway(net.IPv4(127, 0, 0, 1), net.IPv4(127, 0, 0, 1))
	g.port = port
	g.timeout = 2 * time.Second
	return g
}

func TestPCP_MapEncodesRequestAndReadsGrant(t *testing.T) {
	var got []byte
	port := fakePCPServer(t, func(req []byte) []byte {
		got = req
		return pcpAnswer(req, 0, 1800, 40001, net.IPv4(198, 51, 100, 9))
	})
	g := testPCP(port)
	granted, err := g.AddMapping(context.Background(), Mapping{
		Protocol: ProtocolTCP, InternalIP: net.IPv4(192, 168, 1, 20), InternalPort: 7070,
		ExternalPort: 7070, Lease: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if granted.ExternalPort != 40001 || granted.Lease != 30*time.Minute {
		t.Fatalf("granted = %+v", granted)
	}
	if ip, _ := g.ExternalIP(context.Background()); !ip.Equal(net.IPv4(198, 51, 100, 9)) {
		t.Fatalf("external ip = %v", ip)
	}
	// The request: version 2, MAP, an hour, the client as IPv4-mapped IPv6,
	// TCP, internal 7070, suggested external 7070.
	if len(got) != 60 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("header = % x", got[:4])
	}
	if lt := binary.BigEndian.Uint32(got[4:8]); lt != 3600 {
		t.Fatalf("lifetime = %d", lt)
	}
	if ip := net.IP(got[8:24]); !ip.Equal(net.IPv4(192, 168, 1, 20)) {
		t.Fatalf("client ip = %v", ip)
	}
	if got[36] != 6 || binary.BigEndian.Uint16(got[40:42]) != 7070 || binary.BigEndian.Uint16(got[42:44]) != 7070 {
		t.Fatalf("map payload = % x", got[36:44])
	}
}

func TestPCP_DeleteReusesNonceWithZeroLifetime(t *testing.T) {
	var reqs [][]byte
	port := fakePCPServer(t, func(req []byte) []byte {
		reqs = append(reqs, req)
		return pcpAnswer(req, 0, binary.BigEndian.Uint32(req[4:8]), 7070, net.IPv4(198, 51, 100, 9))
	})
	g := testPCP(port)
	m := Mapping{Protocol: ProtocolTCP, InternalIP: net.IPv4(192, 168, 1, 20), InternalPort: 7070, ExternalPort: 7070, Lease: time.Hour}
	if _, err := g.AddMapping(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteMapping(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if lt := binary.BigEndian.Uint32(reqs[1][4:8]); lt != 0 {
		t.Fatalf("delete lifetime = %d, want 0", lt)
	}
	if string(reqs[0][24:36]) != string(reqs[1][24:36]) {
		t.Fatal("delete used a different nonce than the mapping")
	}
}

func TestPCP_RefusalAndNATPMPOnlyServer(t *testing.T) {
	refuse := fakePCPServer(t, func(req []byte) []byte { return pcpAnswer(req, 2, 0, 0, net.IPv4zero) })
	_, err := testPCP(refuse).AddMapping(context.Background(), Mapping{Protocol: ProtocolTCP, InternalIP: net.IPv4(10, 0, 0, 2), InternalPort: 7070, ExternalPort: 7070, Lease: time.Hour})
	var re *pcpResultError
	if !errors.As(err, &re) || re.code != pcpNotAuthorized {
		t.Fatalf("err = %v, want NOT_AUTHORIZED", err)
	}

	// A NAT-PMP-only server answers version 2 with a version-0
	// UNSUPP_VERSION packet.
	pmpOnly := fakePCPServer(t, func(req []byte) []byte { return []byte{0, 0x80 | req[1], 0, 1, 0, 0, 0, 0} })
	_, err = testPCP(pmpOnly).AddMapping(context.Background(), Mapping{Protocol: ProtocolTCP, InternalIP: net.IPv4(10, 0, 0, 2), InternalPort: 7070, ExternalPort: 7070, Lease: time.Hour})
	if !errors.Is(err, errPCPUnsupported) {
		t.Fatalf("err = %v, want errPCPUnsupported", err)
	}
}

func TestPCP_IgnoresOtherNonceAndTimesOut(t *testing.T) {
	port := fakePCPServer(t, func(req []byte) []byte {
		other := append([]byte(nil), req...)
		other[24] ^= 0xff // someone else's mapping
		return pcpAnswer(other, 0, 3600, 7070, net.IPv4(198, 51, 100, 9))
	})
	g := testPCP(port)
	g.timeout = 1500 * time.Millisecond
	start := time.Now()
	_, err := g.AddMapping(context.Background(), Mapping{Protocol: ProtocolTCP, InternalIP: net.IPv4(10, 0, 0, 2), InternalPort: 7070, ExternalPort: 7070, Lease: time.Hour})
	if err == nil {
		t.Fatal("accepted a response for another nonce")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}
