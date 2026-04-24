package scanner

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processMusicVideo creates the artist → music_video hierarchy for a
// video file inside a music library. Music videos attach directly to
// their artist — there's no album between, because a Prince music-
// video collection doesn't map to his albums the way his discography
// does. The artist page gets a "Music Videos" row alongside the
// albums row.
//
// Artist resolution order:
//  1. Folder convention: `<Artist>/Music Videos/<file>` (Plex style)
//     → artist = two folders up
//  2. Folder convention: `<Artist>/<file>` with flat videos under the
//     artist directory → artist = one folder up
//  3. Filename parse: `Artist - Title.ext` → artist = left side
//  4. Fall back to "Unknown Artist" so the file isn't lost
//
// Nothing here hits the tag library — dhowden/tag doesn't read MKV
// or MP4 artist/title tags reliably across the codec zoo that
// music-video rips use, and the folder layout is almost always the
// operator's source of truth anyway.
func (s *Scanner) processMusicVideo(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	artistTitle, videoTitle := parseMusicVideoPath(path, roots)

	// 1. Find or create the parent artist. Artists created here share
	// the same media_items row as audio-track artists — if the user
	// has both songs and music videos by the same performer, the
	// artist row is reused and the artist page shows both rows.
	artist, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "artist",
		Title:     artistTitle,
		SortTitle: sortTitle(artistTitle),
	})
	if err != nil {
		return nil, err
	}

	// 2. Create the music_video item as a direct child of the artist.
	mv, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "music_video",
		Title:     videoTitle,
		SortTitle: sortTitle(videoTitle),
		ParentID:  &artist.ID,
	})
	if err != nil {
		return nil, err
	}
	return mv, nil
}

// parseMusicVideoPath derives the (artist, videoTitle) pair from a
// file path, using folder convention first and filename parsing as
// a fallback. roots is the set of library scan paths — we stop
// walking up the folder tree when we hit one of them so we don't
// accidentally use `/mnt/media` or similar as the artist name.
func parseMusicVideoPath(path string, roots []string) (artist, title string) {
	title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	dir := filepath.Dir(path)
	parent := filepath.Base(dir)
	grandparent := filepath.Base(filepath.Dir(dir))

	// `<Artist>/Music Videos/<file>` — the canonical Plex layout.
	if strings.EqualFold(parent, "Music Videos") || strings.EqualFold(parent, "Videos") {
		if grandparent != "" && !isLibraryRoot(filepath.Dir(dir), roots) {
			artist = grandparent
			title = cleanMusicVideoTitle(title, artist)
			return
		}
	}

	// `<Artist>/<file>` — flat layout with videos directly under the
	// artist folder. Only accept when the parent directory isn't the
	// library root itself (a loose video at the root has no artist).
	if parent != "" && !isLibraryRoot(dir, roots) {
		artist = parent
		title = cleanMusicVideoTitle(title, artist)
		return
	}

	// Filename `Artist - Title.ext` as last resort.
	if i := strings.Index(title, " - "); i > 0 {
		artist = strings.TrimSpace(title[:i])
		title = strings.TrimSpace(title[i+3:])
		return
	}

	artist = "Unknown Artist"
	return
}

// cleanMusicVideoTitle strips a leading "Artist - " from a filename
// when the artist is already known from the folder layout — avoids
// "Prince - Purple Rain" rendering as the video title when the
// artist page already headers "Prince."
func cleanMusicVideoTitle(title, artist string) string {
	prefix := artist + " - "
	if strings.HasPrefix(strings.ToLower(title), strings.ToLower(prefix)) {
		return strings.TrimSpace(title[len(prefix):])
	}
	return title
}

// isLibraryRoot reports whether dir is one of the configured library
// scan paths (or equivalent up to separator normalization). Used to
// stop folder-walk before we treat the root as an artist name.
func isLibraryRoot(dir string, roots []string) bool {
	want := filepath.Clean(dir)
	for _, r := range roots {
		if filepath.Clean(r) == want {
			return true
		}
	}
	return false
}
