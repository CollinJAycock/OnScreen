package scanner

import "testing"

// TestParsePodcastPath_ShowFolder is the canonical layout —
// <root>/<Show>/<episode>.mp3.
func TestParsePodcastPath_ShowFolder(t *testing.T) {
	show, ep := parsePodcastPath(
		"/media/Podcasts/The Daily/2026-04-25 - Episode Title.mp3",
		[]string{"/media/Podcasts"},
	)
	if show != "The Daily" {
		t.Errorf("show = %q, want The Daily", show)
	}
	if ep != "2026-04-25 - Episode Title" {
		t.Errorf("episode = %q", ep)
	}
}

// TestParsePodcastPath_LooseAtRoot covers a file at the library root
// — show falls back to "Unknown Show" so the file isn't lost from
// the grid.
func TestParsePodcastPath_LooseAtRoot(t *testing.T) {
	show, ep := parsePodcastPath(
		"/media/Podcasts/orphan.mp3",
		[]string{"/media/Podcasts"},
	)
	if show != "Unknown Show" {
		t.Errorf("show = %q, want Unknown Show", show)
	}
	if ep != "orphan" {
		t.Errorf("episode = %q, want orphan", ep)
	}
}

// TestFileTypeForLibrary_Podcast confirms podcast libraries default
// items to podcast_episode (not "movie" or "track").
func TestFileTypeForLibrary_Podcast(t *testing.T) {
	if got := fileTypeForLibrary("podcast"); got != "podcast_episode" {
		t.Errorf("fileTypeForLibrary(podcast) = %q, want podcast_episode", got)
	}
}
