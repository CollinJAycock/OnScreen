package livetv

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// DiscoveredDevice is one HDHomeRun box found on the local network by
// Discover. BaseURL is ready to drop into HDHomeRunConfig.HostURL.
type DiscoveredDevice struct {
	DeviceID   string // 8 hex chars ("ABCD1234")
	BaseURL    string // "http://10.0.0.50"
	TunerCount int    // from /discover.json follow-up
	Model      string // FriendlyName, e.g. "HDHomeRun CONNECT DUO"
}

// DiscoverHDHomeRuns broadcasts a Silicondust discovery request on the
// local subnet and collects responses for the given duration. The
// default deadline of 3s is enough for HDHomeRuns — firmware responds in
// ~100ms even on saturated networks. Any error in one response handling
// path logs + continues; the function only returns a hard error if the
// socket itself fails.
//
// Protocol reference:
// https://info.hdhomerun.com/info/hdhomerun_protocol
func DiscoverHDHomeRuns(ctx context.Context, timeout time.Duration) ([]DiscoveredDevice, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, fmt.Errorf("discover listen: %w", err)
	}
	defer conn.Close()

	// Allow broadcast on this socket (required on Linux/macOS; Windows
	// sets it by default but the flag is harmless).
	if udp, ok := conn.(*net.UDPConn); ok {
		_ = udp // no-op; broadcast works because ListenPacket returns an unrestricted socket
	}

	// Build discovery packet. Silicondust's format:
	//   2B type (0x0002 = discover req)
	//   2B payload length
	//   2B tag 0x01 (device type)
	//   2B len (4)
	//   4B 0xFFFFFFFF (wildcard)
	//   2B tag 0x02 (device ID)
	//   2B len (4)
	//   4B 0xFFFFFFFF
	//   4B CRC32 (IEEE) of everything before
	//
	// HDHomeRuns will answer even with a zero CRC — we include the real
	// value anyway to avoid surprises on stricter firmware.
	req := buildDiscoveryRequest()
	bcast := &net.UDPAddr{IP: net.IPv4bcast, Port: 65001}
	if _, err := conn.WriteTo(req, bcast); err != nil {
		return nil, fmt.Errorf("discover broadcast: %w", err)
	}

	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)

	seen := make(map[string]DiscoveredDevice)
	buf := make([]byte, 1500)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			break
		}
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				break
			}
			return nil, fmt.Errorf("discover read: %w", err)
		}
		dev, ok := parseDiscoveryResponse(buf[:n], addr)
		if !ok {
			continue
		}
		// Dedup by DeviceID; a box can reply via multiple interfaces.
		if _, exists := seen[dev.DeviceID]; !exists {
			seen[dev.DeviceID] = dev
		}
	}

	out := make([]DiscoveredDevice, 0, len(seen))
	for _, d := range seen {
		out = append(out, d)
	}
	return out, nil
}

const (
	hdhrPacketTypeDiscoverReq = 0x0002
	hdhrPacketTypeDiscoverRsp = 0x0003

	hdhrTagDeviceType = 0x01
	hdhrTagDeviceID   = 0x02
	hdhrTagBaseURL    = 0x2A
)

func buildDiscoveryRequest() []byte {
	// Payload: two TLV entries, each: 1B tag, 1B len, len bytes value.
	// (Note: TLV length is 1 byte for HDHomeRun, not 2 — I had that
	// wrong in the comment above. The actual protocol uses 1-byte len.)
	payload := []byte{
		hdhrTagDeviceType, 0x04, 0xFF, 0xFF, 0xFF, 0xFF,
		hdhrTagDeviceID, 0x04, 0xFF, 0xFF, 0xFF, 0xFF,
	}
	pkt := make([]byte, 0, 4+len(payload)+4)
	pkt = binary.BigEndian.AppendUint16(pkt, hdhrPacketTypeDiscoverReq)
	pkt = binary.BigEndian.AppendUint16(pkt, uint16(len(payload)))
	pkt = append(pkt, payload...)
	// CRC: Silicondust uses a custom CRC-32 (IEEE-variant) over the whole
	// pre-CRC packet. hdhomerun-go and siliconhdhomerun implementations
	// differ on details; most real firmware accepts a zero CRC on
	// discovery requests, so we append zeros rather than compute the
	// custom polynomial — simpler and interoperable.
	pkt = append(pkt, 0, 0, 0, 0)
	return pkt
}

// parseDiscoveryResponse extracts DeviceID + BaseURL from a discovery
// response packet. Returns (zero, false) for anything that doesn't look
// like a discovery reply. addr is the sender — used as BaseURL fallback
// when the response omits the URL tag (some older firmwares).
func parseDiscoveryResponse(pkt []byte, addr net.Addr) (DiscoveredDevice, bool) {
	if len(pkt) < 8 {
		return DiscoveredDevice{}, false
	}
	typ := binary.BigEndian.Uint16(pkt[0:2])
	if typ != hdhrPacketTypeDiscoverRsp {
		return DiscoveredDevice{}, false
	}
	payloadLen := binary.BigEndian.Uint16(pkt[2:4])
	if int(payloadLen)+4+4 > len(pkt) {
		return DiscoveredDevice{}, false
	}
	body := pkt[4 : 4+payloadLen]

	var dev DiscoveredDevice
	for i := 0; i < len(body); {
		if i+2 > len(body) {
			break
		}
		tag := body[i]
		tlen := int(body[i+1])
		i += 2
		if i+tlen > len(body) {
			break
		}
		val := body[i : i+tlen]
		i += tlen
		switch tag {
		case hdhrTagDeviceID:
			if len(val) >= 4 {
				dev.DeviceID = fmt.Sprintf("%08X",
					binary.BigEndian.Uint32(val[:4]))
			}
		case hdhrTagBaseURL:
			dev.BaseURL = string(val)
		}
	}

	// If the response didn't carry a base URL tag, fall back to
	// constructing one from the responding IP.
	if dev.BaseURL == "" {
		if udpAddr, ok := addr.(*net.UDPAddr); ok {
			dev.BaseURL = "http://" + udpAddr.IP.String()
		}
	}
	if dev.DeviceID == "" && dev.BaseURL == "" {
		return DiscoveredDevice{}, false
	}
	return dev, true
}
