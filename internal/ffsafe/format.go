package ffsafe

import (
	"bytes"
	"io"
	"os"
	"strings"
)

// ── Reference containers by content ────────────────────────────────────────
//
// The protocol allowlist keeps a library file off the network, but file: must
// stay allowed, so a file whose bytes are a DASH / HLS / concat / IMF manifest
// can still make ffmpeg open OTHER local media and stream it back. ffmpeg picks
// those demuxers from content, and its probes look further than a "starts
// with" check: dash_probe finds "<MPD" anywhere in the probe buffer, so a
// manifest behind a 700-byte XML comment passed the old 512-byte sniff.
//
// LooksLikeReferenceFile mirrors what those probes can see. Format detection
// reads at most formatprobesize (1 MiB) and skips one leading ID3v2 tag, and
// the text probes treat the buffer as a C string, so they see nothing past
// its first NUL byte. Real media is binary and has a NUL within a few bytes,
// which also keeps the check cheap; only text has to be read further.

// referenceSniffWindow bounds the bytes read: twice ffmpeg's default
// formatprobesize, for margin.
const referenceSniffWindow = 2 << 20

// LooksLikeReferenceFile reports whether the local file at path holds a
// playlist or reference container that ffmpeg could select from its content.
// false for http(s) URLs and unreadable paths.
func LooksLikeReferenceFile(path string) bool {
	if IsHTTPURL(path) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [10]byte
	n, _ := io.ReadFull(f, hdr[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	if IsReferenceText(textPrefix(f, referenceSniffWindow)) {
		return true
	}
	// ffmpeg hands its probes the buffer after a leading ID3v2 tag.
	if l, ok := id3v2TagLen(hdr[:n]); ok && l+16 < referenceSniffWindow {
		if _, err := f.Seek(l, io.SeekStart); err == nil {
			return IsReferenceText(textPrefix(f, int(referenceSniffWindow-l)))
		}
	}
	return false
}

// IsReferenceText reports whether text (a file's leading bytes up to its first
// NUL) carries the signature of a playlist or reference format: the ones
// ffmpeg recognises at the start (HLS, ffconcat, SDP) or anywhere in the probe
// buffer (DASH "<MPD", IMF "<CompositionPlaylist", an SDP "c=IN IP" line).
// Case-insensitive and whitespace-tolerant: a superset of ffmpeg's probes.
func IsReferenceText(text []byte) bool {
	if i := bytes.IndexByte(text, 0); i >= 0 {
		text = text[:i]
	}
	head := strings.TrimLeft(strings.TrimPrefix(string(text), "\xef\xbb\xbf"), " \t\r\n")
	lower := strings.ToLower(head)
	switch {
	case strings.HasPrefix(head, "#EXTM3U"), strings.HasPrefix(head, "#EXT-X-"):
		return true // HLS / extended M3U playlist
	case strings.HasPrefix(lower, "ffconcat"):
		return true // ffmpeg concat script
	case strings.HasPrefix(head, "v=0\n"), strings.HasPrefix(head, "v=0\r\n"):
		return true // SDP session description
	case strings.Contains(lower, "<mpd"):
		return true // DASH manifest
	case strings.Contains(lower, "<compositionplaylist"):
		return true // IMF composition playlist (opens its track files)
	case strings.HasPrefix(lower, "c=in ip"), strings.Contains(lower, "\nc=in ip"):
		return true // SDP connection line
	}
	return false
}

// textPrefix reads r until its first NUL byte or limit bytes and returns what
// came before the NUL.
func textPrefix(r io.Reader, limit int) []byte {
	var out []byte
	chunk := make([]byte, 4096)
	for len(out) < limit {
		n, err := r.Read(chunk[:min(len(chunk), limit-len(out))])
		if i := bytes.IndexByte(chunk[:n], 0); i >= 0 {
			return append(out, chunk[:i]...)
		}
		out = append(out, chunk[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

// id3v2TagLen is libavformat's ff_id3v2_match + ff_id3v2_tag_len.
func id3v2TagLen(h []byte) (int64, bool) {
	if len(h) < 10 || string(h[:3]) != "ID3" || h[3] == 0xFF || h[4] == 0xFF ||
		h[6]&0x80 != 0 || h[7]&0x80 != 0 || h[8]&0x80 != 0 || h[9]&0x80 != 0 {
		return 0, false
	}
	l := int64(h[6])<<21 | int64(h[7])<<14 | int64(h[8])<<7 | int64(h[9])
	l += 10
	if h[5]&0x10 != 0 {
		l += 10 // footer
	}
	return l, true
}

// ── Demuxer from the scanned container ──────────────────────────────────────

// demuxers maps the container name the scanner records (the first token of
// ffprobe's format_name, or an extension for files recorded without a probe)
// to the demuxer that read it. -format_whitelist is matched against every
// comma-separated alias of a demuxer's name, so the full name is used.
var demuxers = map[string]string{
	"matroska": "matroska,webm", "webm": "matroska,webm", "mkv": "matroska,webm", "mka": "matroska,webm", "mk3d": "matroska,webm",
	"mov": "mov,mp4,m4a,3gp,3g2,mj2", "mp4": "mov,mp4,m4a,3gp,3g2,mj2", "m4a": "mov,mp4,m4a,3gp,3g2,mj2",
	"m4v": "mov,mp4,m4a,3gp,3g2,mj2", "m4b": "mov,mp4,m4a,3gp,3g2,mj2", "3gp": "mov,mp4,m4a,3gp,3g2,mj2",
	"mpegts": "mpegts", "ts": "mpegts", "m2ts": "mpegts", "mts": "mpegts",
	"avi":  "avi",
	"flac": "flac",
	"mp3":  "mp3",
	"ogg":  "ogg", "oga": "ogg", "ogv": "ogg", "opus": "ogg",
	"wav": "wav",
	"aac": "aac",
	"asf": "asf", "wmv": "asf", "wma": "asf",
	"flv":  "flv",
	"mpeg": "mpeg", "vob": "mpeg", "mpg": "mpeg",
	"aiff": "aiff",
	"ape":  "ape",
	"wv":   "wv",
	"dsf":  "dsf",
	"iff":  "iff",
	"tta":  "tta",
	"caf":  "caf",
	"w64":  "w64",
}

// FormatWhitelist returns the -format_whitelist value for a file recorded with
// container, or "" when the container is unknown (no restriction: the caller
// keeps today's content detection rather than refusing playback).
func FormatWhitelist(container string) string {
	return demuxers[strings.ToLower(strings.TrimSpace(container))]
}

// InputArgs is InputProtocolArgs plus, when the container recorded at scan
// time is known, -format_whitelist naming the demuxer that read it, so the
// file can only be opened the way it was indexed: a file replaced in place by
// a manifest can't switch ffmpeg to the dash / hls / concat demuxer. Place
// IMMEDIATELY before the -i it guards.
func InputArgs(input, container string) []string {
	args := InputProtocolArgs(input)
	if fw := FormatWhitelist(container); fw != "" {
		args = append(args, "-format_whitelist", fw)
	}
	return args
}

// ── Piped input ─────────────────────────────────────────────────────────────

// StdinInputArgs returns the input options for a stream piped to ffmpeg's
// stdin (`-i pipe:0`): the demuxer forced to format (a tuner's MPEG-TS when
// the upstream gives no hint) and only the pipe protocol. A stream from a
// tuner or IPTV upstream is as untrusted as a library file, and a piped
// manifest would otherwise be free to open local files; with pipe the only
// protocol and the demuxer fixed, it can open nothing. Place IMMEDIATELY
// before `-i pipe:0`.
func StdinInputArgs(format string) []string {
	if format == "" {
		format = "mpegts"
	}
	return []string{"-f", format, "-protocol_whitelist", "pipe"}
}
