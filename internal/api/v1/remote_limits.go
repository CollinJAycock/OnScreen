package v1

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// RemoteStreamPolicy is the server-wide half of the home-network / internet
// split: the internet streaming limit and any extra networks an admin counts
// as home. Set in Settings ▸ Transcode; read live per play decision.
type RemoteStreamPolicy struct {
	// MaxBitrateKbps caps every remote (internet) stream; 0 = unlimited.
	MaxBitrateKbps int
	// LocalNetworks are admin-added home networks, checked after the
	// built-in classifier (isLocalAddr).
	LocalNetworks []netip.Prefix
}

// RemoteStreamPolicyReader supplies the current RemoteStreamPolicy. Optional
// on the handlers that use it; nil = no internet limit and only the built-in
// home networks. cmd/server backs it with the settings service.
type RemoteStreamPolicyReader interface {
	RemoteStreamPolicy(ctx context.Context) RemoteStreamPolicy
}

// readRemotePolicy is nil-safe: no reader = the zero policy.
func readRemotePolicy(ctx context.Context, r RemoteStreamPolicyReader) RemoteStreamPolicy {
	if r == nil {
		return RemoteStreamPolicy{}
	}
	return r.RemoteStreamPolicy(ctx)
}

// IsLocal reports whether ip (a bare address, as audit.ClientIP returns it —
// the trusted client IP TrustedRealIP resolved, never a raw forwarded header)
// is on the home network: the built-in set isLocalAddr recognises (loopback,
// RFC 1918 / unique-local, link-local, 100.64.0.0/10 — Tailscale / CGNAT —
// and this host's own IPv6 prefixes) or one of the admin's extra networks.
// An unparseable address is remote: the limit fails closed.
//
// Behind a reverse proxy this is only as good as the proxy's forwarding: a
// trusted proxy that sends no X-Forwarded-For leaves its own (private)
// address as the client, and every stream reads as local. The bundled nginx
// sample and cloudflared both forward it.
func (p RemoteStreamPolicy) IsLocal(ip string) bool {
	if isLocalAddr(ip) {
		return true
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, n := range p.LocalNetworks {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// ParseLocalNetworks parses the admin's extra home networks: CIDRs, or bare
// IPs taken as a single host. Blank entries are skipped. Returns an error
// naming the first bad entry, so the settings API can reject a typo instead
// of silently treating nothing (or the wrong thing) as local.
func ParseLocalNetworks(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range entries {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or CIDR", s)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// streamCap is the bitrate ceiling one playback request is held to.
type streamCap struct {
	// Kbps is the effective cap; 0 = none.
	Kbps int
	// Remote reports the client is outside the home network.
	Remote bool
	// RemoteBinding reports an internet limit (the user's or the server's)
	// is what sets Kbps — i.e. on the LAN this stream would be allowed more.
	// It picks the reason shown to the client and on Now Playing.
	RemoteBinding bool
	// RemoteLimited reports an internet limit applies to this request at all
	// (remote video, with the user's or the server's internet cap set). Only
	// then does the play decision enforce Kbps: a LAN client — or a remote one
	// with no internet limit configured — gets exactly the decision it got
	// before internet limits existed.
	RemoteLimited bool
}

// effectiveStreamCap combines the per-user caps with the server's internet
// limit. A LAN stream keeps only the user's cap. A remote stream gets the
// lowest non-zero of the user's cap, the user's internet cap and the server's
// internet limit — the internet limits apply to video only, so music,
// audiobooks and podcasts stay bit-perfect over the internet (they never
// approach a video bitrate cap anyway).
func effectiveStreamCap(user StreamCaps, policy RemoteStreamPolicy, remote, hasVideo bool) streamCap {
	c := streamCap{Kbps: user.BitrateKbps, Remote: remote}
	if !remote || !hasVideo {
		return c
	}
	for _, r := range []int{user.RemoteBitrateKbps, policy.MaxBitrateKbps} {
		if r <= 0 {
			continue
		}
		c.RemoteLimited = true
		if c.Kbps == 0 || r < c.Kbps {
			c.Kbps = r
			c.RemoteBinding = true
		}
	}
	return c
}

// resolveStreamCap reads both halves of the cap for one request: the user's
// admin-set caps and, from the trusted client IP, whether it is remote.
func resolveStreamCap(ctx context.Context, user StreamCaps, policies RemoteStreamPolicyReader, clientIP string, file *media.File) streamCap {
	policy := readRemotePolicy(ctx, policies)
	hasVideo := file != nil && file.VideoCodec != nil && *file.VideoCodec != ""
	return effectiveStreamCap(user, policy, !policy.IsLocal(clientIP), hasVideo)
}

// overCap reports whether a file's source bitrate exceeds the cap (unknown
// bitrate or no cap = not over).
func (c streamCap) overCap(file *media.File) bool {
	return c.Kbps > 0 && file != nil && file.Bitrate != nil && int(*file.Bitrate/1000) > c.Kbps
}

// Playback-decision reason codes for a remote client forced to transcode by
// its bitrate cap: remote_bitrate_limit when an internet limit is the binding
// ceiling, user_bitrate_limit when the user's own (lower) cap is.
const (
	ReasonRemoteBitrateLimit = "remote_bitrate_limit"
	ReasonUserBitrateLimit   = "user_bitrate_limit"
)

// remoteDecisionOverride is the play decision's internet-limit check: a
// directPlay / directStream verdict for a remote client whose source exceeds
// its cap becomes transcode (Start then clamps the output to the cap). Returns
// ok=false — leave the decision alone — for LAN clients, remote clients with
// no internet limit set, audio, and sources under the cap.
func remoteDecisionOverride(bc streamCap, file *media.File) (reason string, ok bool) {
	if !bc.RemoteLimited || !bc.overCap(file) {
		return "", false
	}
	if bc.RemoteBinding {
		return ReasonRemoteBitrateLimit, true
	}
	return ReasonUserBitrateLimit, true
}
