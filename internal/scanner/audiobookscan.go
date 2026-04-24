package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// processAudiobook creates an 'audiobook' media_item for a file in an
// audiobook library. v2.0 scope is flat — one file = one row — with
// author / title pulled from (in priority order) embedded tags,
// folder layout, and filename parse. Chapter navigation comes from
// ffprobe's container-level chapter markers at playback time.
//
// Folder convention (Audiobookshelf / Jellyfin style):
//
//	<Library>/<Author>/<Book>/<file>.m4b
//	<Library>/<Author>/<Book Name>.m4b
//	<Library>/<Book Name>.m4b
//
// The series hierarchy (author → series → book) is deliberately
// deferred to v2.1 so users can start organizing their library
// immediately; a later migration can reparent existing rows without
// rescanning.
func (s *Scanner) processAudiobook(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	title, author := parseAudiobookPath(path, roots)

	// Read embedded tags (m4b / MP3 ID3 / FLAC Vorbis) — title and
	// author there beat the folder guess when present. Missing tags
	// aren't fatal; the folder-derived values stay as the fallback.
	if f, err := os.Open(path); err == nil {
		if m, err := readTagFrom(f); err == nil {
			if t := strings.TrimSpace(m.Title()); t != "" {
				title = t
			}
			// Audiobook rippers use either <artist> or <album_artist>
			// for the author — either is acceptable.
			if a := strings.TrimSpace(m.AlbumArtist()); a != "" {
				author = a
			} else if a := strings.TrimSpace(m.Artist()); a != "" {
				author = a
			}
		}
		_ = f.Close()
	}

	if title == "" {
		title = filepath.Base(path)
	}

	p := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "audiobook",
		Title:     title,
		SortTitle: sortTitle(title),
	}
	// Stash the author on the OriginalTitle field for now — it's
	// already in the item schema, the UI surfaces it cleanly, and
	// it avoids a migration just to add a dedicated column. A
	// proper author hierarchy in v2.1 migrates these values into a
	// parent `audiobook_author` row.
	if author != "" {
		p.OriginalTitle = &author
	}

	item, err := s.media.FindOrCreateItem(ctx, p)
	if err != nil {
		return nil, err
	}
	return item, nil
}

// parseAudiobookPath returns (title, author) derived from the directory
// layout. Precedence:
//
//	<root>/<Author>/<Book>/<file>   → author from grandparent dir,
//	                                  title from parent dir
//	<root>/<Author>/<file>          → author from parent dir,
//	                                  title from filename stem
//	<root>/<file>                   → no author, title from filename
//
// The roots guard keeps us from using `/mnt/media/Audiobooks` as the
// author when the library root happens to be the penultimate path
// element.
func parseAudiobookPath(path string, roots []string) (title, author string) {
	title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	dir := filepath.Dir(path)
	parent := filepath.Base(dir)
	grand := filepath.Dir(dir)
	greatGrand := filepath.Dir(grand)

	// Loose file at the library root — no author context, title stays
	// as the filename stem.
	if isLibraryRoot(dir, roots) {
		return
	}

	// Two-level nesting: <root>/<Author>/<Book>/<file>. The great-
	// grandparent path equals the library root.
	if isLibraryRoot(greatGrand, roots) {
		author = filepath.Base(grand)
		title = parent
		return
	}

	// Single-level nesting: <root>/<Author>/<file>. The grandparent
	// path equals the library root.
	if isLibraryRoot(grand, roots) {
		author = parent
		return
	}

	// Deeper than two levels — fall back to "nearest non-root
	// ancestor is the author." Titles stay as the filename stem.
	author = parent
	return
}
