package media

import (
	"path/filepath"
	"testing"
)

func TestSanitizeFilenameStem(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Birthday Party", "Birthday Party"},
		{"empty", "", "untitled"},
		{"all-stripped", "<>|?*", "untitled"},
		{"colons replaced", "Brand: New Day", "Brand New Day"},
		{"path separators replaced", "a/b\\c", "a b c"},
		{"control chars stripped", "Hello\x00\x01World", "HelloWorld"},
		{"trailing dots", "Trip.", "Trip"},
		{"trailing spaces", "Trip   ", "Trip"},
		{"trailing dot-space mix", "Trip. .  ", "Trip"},
		{"runs of whitespace collapsed", "A   B\tC", "A B C"},
		{"unicode preserved", "Yellowstone — 2024", "Yellowstone — 2024"},
		{"reserved CON", "CON", "_CON"},
		{"reserved con lowercase", "con", "_con"},
		{"reserved COM1", "COM1", "_COM1"},
		{"reserved LPT9", "LPT9", "_LPT9"},
		{"COM with non-digit ok", "COMA", "COMA"},
		{"long title truncated", longString(300, 'a'), longString(200, 'a')},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sanitizeFilenameStem(c.in); got != c.want {
				t.Errorf("sanitizeFilenameStem(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestUniqueRenamePath_NoCollision(t *testing.T) {
	got := uniqueRenamePath("/m", "Trip", ".mp4", func(string) bool { return false })
	want := filepath.Join("/m", "Trip.mp4")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUniqueRenamePath_CollidesAndSuffixes(t *testing.T) {
	taken := map[string]bool{
		filepath.Join("/m", "Trip.mp4"):     true,
		filepath.Join("/m", "Trip (2).mp4"): true,
	}
	got := uniqueRenamePath("/m", "Trip", ".mp4", func(p string) bool { return taken[p] })
	want := filepath.Join("/m", "Trip (3).mp4")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func longString(n int, c byte) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}
