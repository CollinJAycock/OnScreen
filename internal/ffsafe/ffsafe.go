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

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

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

// ── Server-built concat lists (stacked movies) ──────────────────────────────
//
// The concat demuxer is exactly the reference-following format the rest of
// this package keeps away from library content: a file whose bytes are an
// ffconcat script makes ffmpeg open every path or URL it names. The scanner
// and every playback gate refuse such files as media (scanner.ErrUnsafeContainer)
// and nothing here changes that — InputProtocolArgs never names a demuxer, so a
// library file is still never opened as concat on purpose.
//
// The one sanctioned use is a list the SERVER writes: the parts of a stacked
// movie ("cd1" + "cd2"), resolved from the database, played as one stream. It
// is written by WriteConcatList into a private directory and opened with
// ConcatInputArgs, which forces the concat demuxer for that list only. Both
// refuse any entry that isn't a plain absolute path or an http(s) URL, so
// `-safe 0` (needed for absolute paths and URLs) can't be turned into a way to
// reach another protocol, and a list mixing local paths and URLs is refused so
// a local part never runs under the network allowlist.

// ConcatPart is one entry of a server-built concat list: a part file's local
// path or its http(s) source URL, and its duration in seconds (0 = unknown,
// ffmpeg then reads it from the file).
type ConcatPart struct {
	Input       string
	DurationSec float64
}

// ConcatInputArgs returns the input options to place IMMEDIATELY before
// `-i <list>` for a list WriteConcatList wrote from parts: the forced concat
// demuxer, `-safe 0`, and the protocol allowlist the entries need — the
// network stack only when every entry is an http(s) URL.
//
// parts that WriteConcatList would refuse get the concat demuxer in its
// default safe mode under the local allowlist instead: safe mode rejects
// absolute paths and URLs, so ffmpeg fails to open such a list rather than
// following it.
func ConcatInputArgs(parts []ConcatPart) []string {
	network, err := checkConcatParts(parts)
	if err != nil {
		return []string{"-f", "concat", "-protocol_whitelist", localProtocols}
	}
	proto := localProtocols
	if network {
		proto = networkProtocols
	}
	return []string{"-f", "concat", "-safe", "0", "-protocol_whitelist", proto}
}

// WriteConcatList writes an ffconcat script naming parts, in order, to path.
// Each entry is single-quoted with ffconcat's escaping; a known duration is
// written too, so ffmpeg can seek across parts without opening every one.
func WriteConcatList(path string, parts []ConcatPart) error {
	if _, err := checkConcatParts(parts); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, p := range parts {
		// Inside single quotes nothing is special but the quote itself,
		// which closes, gets a backslash-escaped quote, and reopens.
		b.WriteString("file '" + strings.ReplaceAll(p.Input, "'", `'\''`) + "'\n")
		if p.DurationSec > 0 {
			b.WriteString("duration " + strconv.FormatFloat(p.DurationSec, 'f', 3, 64) + "\n")
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// checkConcatParts validates a server-built concat list and reports whether
// it is all http(s) URLs (true) or all local paths (false).
func checkConcatParts(parts []ConcatPart) (network bool, err error) {
	if len(parts) == 0 {
		return false, errors.New("ffsafe: empty concat list")
	}
	for i, p := range parts {
		if p.Input == "" || strings.ContainsAny(p.Input, "\r\n\x00") {
			return false, fmt.Errorf("ffsafe: concat entry %d is empty or holds a line break", i)
		}
		isURL := IsHTTPURL(p.Input)
		if !isURL && !isAbsLocalPath(p.Input) {
			return false, fmt.Errorf("ffsafe: concat entry %d is neither an absolute path nor an http(s) URL", i)
		}
		if i == 0 {
			network = isURL
		} else if isURL != network {
			return false, errors.New("ffsafe: concat list mixes local paths and URLs")
		}
	}
	return network, nil
}

// isAbsLocalPath accepts a POSIX absolute path, a Windows drive path or a UNC
// path — never a "proto:" string ffmpeg would read as a protocol.
func isAbsLocalPath(s string) bool {
	switch {
	case strings.HasPrefix(s, "/"):
		return true
	case strings.HasPrefix(s, `\\`):
		return true
	case len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') &&
		((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')):
		return true
	}
	return false
}
