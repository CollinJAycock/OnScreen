package api

import (
	"net/http/httptest"
	"strconv"
	"testing"
)

// The /artwork/* route is the only place caller-supplied dimensions reach the
// resize cache, which is keyed by source path + WxH and never evicted. If the
// route stops snapping, a single authenticated user can walk ?w= × ?h= and have
// the server encode and store a distinct JPEG for every pair.
func TestArtworkResizeDims_SnapsCallerInput(t *testing.T) {
	cases := []struct {
		query string
		wantW int
		wantH int
		why   string
	}{
		{"", 0, 0, "no params means unconstrained on both axes"},
		{"?w=300&h=450", 300, 450, "sizes the clients actually request pass through exactly"},
		{"?w=301", 320, 0, "an off-ladder width snaps up to the next bucket"},
		{"?w=1&h=2", 120, 120, "tiny values collapse onto the smallest bucket"},
		{"?w=99999&h=99999", 1920, 1920, "the ladder tops out at 1920, subsuming the old clamp"},
		{"?w=-5", 0, 0, "a negative is not a size"},
		{"?w=abc", 0, 0, "unparseable falls back to unconstrained"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/artwork/poster.jpg"+c.query, nil)
		gotW, gotH := artworkResizeDims(req)
		if gotW != c.wantW || gotH != c.wantH {
			t.Errorf("%q: got %dx%d, want %dx%d — %s",
				c.query, gotW, gotH, c.wantW, c.wantH, c.why)
		}
	}
}

// Sweeping the whole accepted range must land on a small number of distinct
// (w,h) pairs — that count, squared, is the cache's per-image ceiling.
func TestArtworkResizeDims_BoundsDistinctSizes(t *testing.T) {
	seen := make(map[int]struct{})
	for v := -10; v <= 4000; v++ {
		req := httptest.NewRequest("GET",
			"/artwork/poster.jpg?w="+strconv.Itoa(v), nil)
		w, _ := artworkResizeDims(req)
		seen[w] = struct{}{}
	}
	if len(seen) > 32 {
		t.Errorf("a sweep of ?w= produced %d distinct widths; the cache holds "+
			"that many entries squared per source image", len(seen))
	}
}
