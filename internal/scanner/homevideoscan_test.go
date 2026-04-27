package scanner

import "testing"

// TestParseHomeVideoTitle_StripsLeadingDate covers the common pattern
// where users prefix filenames with a date for their own ordering.
// Without stripping, "2024-04-15 - Yellowstone hike" would surface
// as the entire raw stem in the library grid.
func TestParseHomeVideoTitle_StripsLeadingDate(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		// Hyphenated date
		{"/media/Home Videos/2024-04-15 - Yellowstone hike.mp4", "Yellowstone hike"},
		// Underscored date
		{"/media/Home Videos/2024_04_15 Birthday party.mov", "Birthday party"},
		// No separator
		{"/media/Home Videos/20240415 Concert.mp4", "Concert"},
		// Em-dash separator
		{"/media/Home Videos/2024-04-15 — Hike.mp4", "Hike"},
		// No date prefix at all — title preserved
		{"/media/Home Videos/Vacation 2024.mp4", "Vacation 2024"},
		// Underscores in title get normalised to spaces
		{"/media/Home Videos/family_trip_2024.mp4", "family trip 2024"},
	}
	for _, c := range cases {
		got := parseHomeVideoTitle(c.path)
		if got != c.want {
			t.Errorf("parseHomeVideoTitle(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// TestParseHomeVideoTitle_EmptyAfterStrip handles the degenerate case
// where the filename is *only* a date — scanner falls back to a
// "Home Video" placeholder rather than creating an empty-title row.
func TestParseHomeVideoTitle_EmptyAfterStrip(t *testing.T) {
	got := parseHomeVideoTitle("/media/Home Videos/2024-04-15.mp4")
	if got != "" {
		t.Errorf("expected empty title for date-only filename, got %q", got)
	}
}
