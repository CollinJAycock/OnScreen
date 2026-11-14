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

// ── Piped input ─────────────────────────────────────────────────────────────

// liveStreamDemuxers are the demuxers a piped live upstream may use when it
// names none: MPEG-TS from tuners and most IPTV, FLV, progressive Matroska /
// MP4 / MPEG-PS, and the MP3 / AAC / Ogg of internet radio. Manifest and
// reference demuxers (dash, hls, concat, imf, …) are absent, so a piped
// manifest fails with "Format not on whitelist" instead of opening the files
// it names. Forcing -f mpegts instead would refuse the radio and progressive
// streams that autodetection plays today.
const liveStreamDemuxers = "mpegts,flv,matroska,webm,mov,mp4,m4a,3gp,3g2,mj2,mpeg,mp3,aac,ogg"

// StdinInputArgs returns the input options for a stream piped to ffmpeg's
// stdin (`-i pipe:0`): only the pipe protocol, and the demuxer forced to
// format when the upstream names one (the RTMP reader's "flv"), else
// autodetection limited to liveStreamDemuxers. A stream from a tuner or IPTV
// upstream is as untrusted as a library file, and a piped manifest would
// otherwise be free to open local files; with pipe the only protocol and no
// manifest demuxer allowed, it can open nothing. Place IMMEDIATELY before
// `-i pipe:0`.
func StdinInputArgs(format string) []string {
	if format != "" {
		return []string{"-f", format, "-protocol_whitelist", "pipe"}
	}
	return []string{"-format_whitelist", liveStreamDemuxers, "-protocol_whitelist", "pipe"}
}
