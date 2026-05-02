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

// TestEventFolderName covers the folder-derivation rules for the
// auto event_folder collection feature: top-level subfolder of a
// configured root wins, multi-level subfolders collapse to that same
// top-level (so a "Yellowstone 2024/Day 1/" trip stays one collection
// instead of splitting per day), and files at root return "" so they
// don't create a junk single-file collection.
func TestEventFolderName(t *testing.T) {
	cases := []struct {
		name  string
		path  string
		roots []string
		want  string
	}{
		{
			name:  "single-level event folder",
			path:  "/media/Home Videos/Yellowstone 2024/clip.mp4",
			roots: []string{"/media/Home Videos"},
			want:  "Yellowstone 2024",
		},
		{
			name:  "multi-level — top-level wins",
			path:  "/media/Home Videos/Yellowstone 2024/Day 1/clip.mp4",
			roots: []string{"/media/Home Videos"},
			want:  "Yellowstone 2024",
		},
		{
			name:  "deeper nesting still collapses to top-level",
			path:  "/media/Home Videos/Yellowstone 2024/Day 1/Sub/clip.mp4",
			roots: []string{"/media/Home Videos"},
			want:  "Yellowstone 2024",
		},
		{
			name:  "loose at root — no collection",
			path:  "/media/Home Videos/orphan.mp4",
			roots: []string{"/media/Home Videos"},
			want:  "",
		},
		{
			name:  "file outside any root — no collection",
			path:  "/elsewhere/clip.mp4",
			roots: []string{"/media/Home Videos"},
			want:  "",
		},
		{
			name:  "trailing slash on root — normalises",
			path:  "/media/Home Videos/Trip/clip.mp4",
			roots: []string{"/media/Home Videos/"},
			want:  "Trip",
		},
		{
			name:  "multiple roots — picks the matching one",
			path:  "/mnt/b/Trip/clip.mp4",
			roots: []string{"/mnt/a", "/mnt/b"},
			want:  "Trip",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := eventFolderName(c.path, c.roots); got != c.want {
				t.Errorf("eventFolderName(%q, %v) = %q, want %q", c.path, c.roots, got, c.want)
			}
		})
	}
}
