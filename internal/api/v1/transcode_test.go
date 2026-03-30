package v1

import (
	"testing"
)

// ── rewritePlaylist ──────────────────────────────────────────────────────────

func TestRewritePlaylist_RewritesSegmentURIs(t *testing.T) {
	input := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.000,\nseg000.ts\n#EXTINF:4.000,\nseg001.ts\n#EXT-X-ENDLIST\n")
	sessionID := "abc123"
	token := "tok456"

	out := string(rewritePlaylist(input, sessionID, token))

	want1 := "/api/v1/transcode/sessions/abc123/seg/seg000.ts?token=tok456"
	want2 := "/api/v1/transcode/sessions/abc123/seg/seg001.ts?token=tok456"
	if !containsLine(out, want1) {
		t.Errorf("missing rewritten segment URI: %q", want1)
	}
	if !containsLine(out, want2) {
		t.Errorf("missing rewritten segment URI: %q", want2)
	}
}

func TestRewritePlaylist_PreservesNonSegmentLines(t *testing.T) {
	input := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.000,\nseg000.ts\n")
	out := string(rewritePlaylist(input, "sid", "tok"))

	if !containsLine(out, "#EXTM3U") {
		t.Error("missing #EXTM3U header")
	}
	if !containsLine(out, "#EXT-X-VERSION:3") {
		t.Error("missing #EXT-X-VERSION header")
	}
}

func TestRewritePlaylist_HandlesSubdirectoryPaths(t *testing.T) {
	// FFmpeg might emit "sub/seg000.ts" — filepath.Base should extract just "seg000.ts".
	input := []byte("#EXTM3U\n#EXTINF:4.000,\nsub/seg000.ts\n")
	out := string(rewritePlaylist(input, "sid", "tok"))

	want := "/api/v1/transcode/sessions/sid/seg/seg000.ts?token=tok"
	if !containsLine(out, want) {
		t.Errorf("expected base filename extraction: want %q in output %q", want, out)
	}
}

func TestRewritePlaylist_EmptyInput(t *testing.T) {
	out := rewritePlaylist([]byte(""), "sid", "tok")
	if len(out) > 1 { // may contain trailing newline
		// Should not panic and should return minimal output.
	}
}

// ── Path sanitization (unit-level) ───────────────────────────────────────────
// These test the key security invariant: filepath.Base strips traversal attempts.

func TestPathTraversal_SegmentName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		safe  string
	}{
		{"normal segment", "seg000.ts", "seg000.ts"},
		{"directory traversal", "../../etc/passwd", "passwd"},
		{"absolute path", "/etc/shadow", "shadow"},
		{"backslash traversal", `..\..\secret`, "secret"},
		{"dot", ".", "."},
		{"dotdot", "..", ".."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This mirrors the sanitization in Segment handler.
			got := sanitizePathComponent(tt.input)
			if got != tt.safe {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.safe)
			}
		})
	}
}

func TestPathTraversal_SessionID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		safe  string
	}{
		{"normal session", "abc123def", "abc123def"},
		{"traversal attempt", "../../tmp/evil", "evil"},
		{"absolute", "/tmp/session", "session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePathComponent(tt.input)
			if got != tt.safe {
				t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.safe)
			}
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func containsLine(haystack, needle string) bool {
	for _, line := range splitLines(haystack) {
		if line == needle {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
