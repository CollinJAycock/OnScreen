// Package ffsafe centralizes the input protocol allowlist passed to ffmpeg and
// ffprobe.
//
// ffmpeg selects a demuxer from an input's CONTENT, not its filename, and some
// demuxers (HLS, concat, and the many that resolve secondary URLs) will follow
// references embedded in that content. With no restriction, a file placed in a
// library — content the threat model treats as attacker-controlled, since *arr
// grabs it off the internet — whose bytes are actually a playlist can make the
// server open http(s) URLs (SSRF: cloud metadata, internal services) or other
// local files while producing output that is handed back to a user.
//
// Passing -protocol_whitelist as an INPUT option (immediately before the -i it
// guards) constrains every protocol the demuxer chain may open for that input.
// A local media file needs only file-family protocols, so the network stack is
// removed entirely — closing the SSRF/exfiltration class. A genuinely remote
// input (the fleet HTTP source, whose URL the server itself minted) additionally
// needs the http stack, so it is granted only when the input is itself an
// http(s) URL. A local file therefore can never pivot to the network.
//
// This does not, by itself, stop a crafted local file from referencing ANOTHER
// local file via file: — closing that fully requires forcing the demuxer with
// -f from the probed container, tracked separately. The network vector is the
// higher-severity one and is closed here.
package ffsafe

import "strings"

// localProtocols is the allowlist for an on-disk input: the file family plus the
// pipe used by stdin-fed inputs (e.g. live-TV muxing). No http, tcp or tls, so a
// crafted local file cannot reach the network.
//
//   - file:    the input itself and legitimate local sidecars
//   - pipe:    stdin inputs (-i pipe:0 / -i -)
//   - crypto:  AES-encrypted fMP4/TS segment internals
//   - data:    RFC 2397 data: URIs embedded by some muxers
//   - subfile: byte-range sub-streams ffmpeg uses internally for some containers
const localProtocols = "file,pipe,crypto,data,subfile"

// networkProtocols additionally permits the http stack, for inputs that are
// themselves remote by construction (the fleet HTTP source the primary hands a
// worker). tcp/tls back http/https; https is included for TLS origins.
const networkProtocols = localProtocols + ",http,https,tcp,tls"

// IsHTTPURL reports whether input is an http(s) URL rather than a local path.
// Exported so callers that make their OWN network-vs-local decision (e.g.
// enabling ffmpeg reconnect flags) key it off the SAME predicate that grants the
// http protocol stack, rather than a looser scheme check that could enable
// networking without the matching protocol grant.
func IsHTTPURL(input string) bool {
	return strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://")
}

// InputProtocolArgs returns the -protocol_whitelist option to place IMMEDIATELY
// before the -i it guards. It permits the network stack only when input is
// itself an http(s) URL; every local or stdin input is confined to file-family
// protocols. Placement matters: -protocol_whitelist is an input option and only
// governs the next -i, so it must precede that -i (like -ss / -reconnect).
func InputProtocolArgs(input string) []string {
	proto := localProtocols
	if IsHTTPURL(input) {
		proto = networkProtocols
	}
	return []string{"-protocol_whitelist", proto}
}

// Whitelist returns the raw comma-separated protocol list InputProtocolArgs
// would use for input, for callers that assemble the flag themselves.
func Whitelist(input string) string {
	if IsHTTPURL(input) {
		return networkProtocols
	}
	return localProtocols
}
