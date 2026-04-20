// Package scanner - musicscan implements the artist->album->track hierarchy
// creation for music library scanning. It reads audio tags, creates parent
// items (artist, album) as needed, and extracts embedded album artwork.
package scanner

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	_ "image/png" // register PNG decoder
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dhowden/tag"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// collabArtistRE matches the tail portion of a collaboration-style artist tag
// like "Elton John & Bonnie Raitt", "Jay-Z feat. Rihanna",
// "The Black Eyed Peas, CL", or "Beyonce / Jay-Z". The regex accepts a comma,
// slash, "&", "and", "feat[.]?", "ft[.]?", "featuring", and "with" as collab
// markers. Stripping is applied only when the primary name (the text before
// the first marker) already exists as a standalone artist in the library —
// otherwise legitimate band names like "Simon & Garfunkel" or
// "Nick Cave and the Bad Seeds" would be damaged.
var collabArtistRE = regexp.MustCompile(`(?i)(\s*,\s*|\s*/\s*|\s+(?:&|and|feat\.?|ft\.?|featuring|with)\s+).+$`)

// lastFirstRE matches "Last, First" pairs commonly produced by classical/jazz
// taggers (e.g. "Dylan, Bob", "Mitchell, Joni"). Both halves must be a single
// token so genuine collab tags like "Beyonce, Jay-Z" aren't miscategorized —
// those still fall through to collabArtistRE via the comma branch.
var lastFirstRE = regexp.MustCompile(`^([^,\s]+),\s+([^,\s]+)$`)

// primaryArtistName strips a trailing collaborator from an artist string.
// Returns "" if no collab marker is found. Callers should verify the result
// exists as an artist before using it; see Scanner.resolveArtistTitle.
func primaryArtistName(s string) string {
	trimmed := collabArtistRE.ReplaceAllString(s, "")
	if trimmed == s {
		return ""
	}
	return strings.TrimSpace(trimmed)
}

// flipLastFirst rewrites "Last, First" to "First Last". Returns "" if the
// input doesn't match the pattern so callers can detect no-op cases.
func flipLastFirst(s string) string {
	m := lastFirstRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[2] + " " + m[1]
}

// resolveArtistTitle returns the artist title to use for a track's hierarchy.
// Two corrections are applied in order:
//  1. "Last, First" → "First Last" (e.g. "Dylan, Bob" → "Bob Dylan"), so
//     taggers that write surname-first don't shard the catalog.
//  2. Collab tag → primary name, if the primary already exists as a
//     standalone artist ("Elton John & Bonnie Raitt" → "Elton John").
//
// If neither applies, the tag is returned unchanged so legitimate multi-name
// bands like "Simon & Garfunkel" are preserved.
func (s *Scanner) resolveArtistTitle(ctx context.Context, libraryID uuid.UUID, tagArtist string) string {
	if flipped := flipLastFirst(tagArtist); flipped != "" {
		tagArtist = flipped
	}
	primary := primaryArtistName(tagArtist)
	if primary == "" {
		return tagArtist
	}
	existing, err := s.media.FindTopLevelItem(ctx, libraryID, "artist", primary)
	if err != nil || existing == nil {
		return tagArtist
	}
	return existing.Title
}

// processMusicHierarchy reads tags from a music file and ensures the
// artist -> album -> track hierarchy exists. Returns the track item.
func (s *Scanner) processMusicHierarchy(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, error) {
	tags, err := ReadMusicTags(path)
	if err != nil {
		return nil, err
	}

	// 1. Find or create the artist (top-level, parent_id=null).
	artistTitle := s.resolveArtistTitle(ctx, libraryID, tags.Artist)
	artist, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "artist",
		Title:     artistTitle,
		SortTitle: sortTitle(artistTitle),
	})
	if err != nil {
		return nil, err
	}

	// 2. Find or create the album (parent_id=artist.id).
	var albumYear *int
	if tags.Year != 0 {
		albumYear = &tags.Year
	}
	album, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "album",
		Title:     tags.Album,
		SortTitle: sortTitle(tags.Album),
		Year:      albumYear,
		ParentID:  &artist.ID,
	})
	if err != nil {
		return nil, err
	}

	// 3. Find or create the track (parent_id=album.id, index=track number).
	var trackIndex *int
	if tags.Track > 0 {
		trackIndex = &tags.Track
	}
	var genres []string
	if tags.Genre != "" {
		genres = []string{tags.Genre}
	}
	track, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "track",
		Title:     tags.Title,
		SortTitle: sortTitle(tags.Title),
		Year:      albumYear,
		ParentID:  &album.ID,
		Index:     trackIndex,
		Genres:    genres,
	})
	if err != nil {
		return nil, err
	}

	// 4. Extract embedded album art if available and poster is missing or stale.
	// Artist posters come from the metadata enricher (TheAudioDB), not album art.
	if tags.AlbumArt {
		s.extractAlbumArt(ctx, album, path, roots)
	}

	return track, nil
}

// extractAlbumArt reads the embedded picture from a music file and writes
// poster.jpg next to the music file (in the album directory).
// On success it updates the album item's poster_path and returns the relative path.
func (s *Scanner) extractAlbumArt(ctx context.Context, album *media.Item, filePath string, roots []string) string {
	artData, err := readEmbeddedArtwork(filePath)
	if err != nil || len(artData) == 0 {
		return ""
	}

	// Store poster.jpg in the same directory as the music file.
	absDir := filepath.Dir(filePath)
	posterFile := filepath.Join(absDir, "poster.jpg")

	// Compute a path relative to the library root for DB storage.
	// The /artwork/* route resolves this against library scan_paths.
	relPath := ""
	for _, root := range roots {
		if rel, err := filepath.Rel(root, posterFile); err == nil && !strings.HasPrefix(rel, "..") {
			relPath = filepath.ToSlash(rel)
			break
		}
	}
	if relPath == "" {
		// Fallback: use album-dir/poster.jpg (may not resolve correctly).
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(absDir), "poster.jpg"))
	}

	// If poster.jpg already exists on disk, just ensure the DB path is correct.
	if _, err := os.Stat(posterFile); err == nil {
		if album.PosterPath == nil || *album.PosterPath != relPath {
			s.updateAlbumPoster(ctx, album, relPath)
		}
		return relPath
	}

	// The embedded art may be PNG or JPEG. We always write JPEG for consistency.
	// Try to decode as an image and re-encode; if that fails write raw bytes.
	var outData []byte
	img, imgErr := decodeImageBytes(artData)
	if imgErr == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			outData = buf.Bytes()
		}
	}
	if outData == nil {
		// Could not re-encode — write the raw bytes (may be JPEG already).
		outData = artData
	}

	if err := os.WriteFile(posterFile, outData, 0o644); err != nil {
		s.logger.WarnContext(ctx, "failed to write album art",
			"album_id", album.ID, "err", err)
		return ""
	}

	s.updateAlbumPoster(ctx, album, relPath)
	return relPath
}

// updateAlbumPoster sets the album's poster_path in the database.
func (s *Scanner) updateAlbumPoster(ctx context.Context, album *media.Item, relPath string) {
	// Normalize to forward slashes for cross-platform consistency.
	relPath = filepath.ToSlash(relPath)
	if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
		ID:         album.ID,
		Title:      album.Title,
		SortTitle:  album.SortTitle,
		Year:       album.Year,
		PosterPath: &relPath,
	}); err != nil {
		s.logger.WarnContext(ctx, "failed to update album poster_path",
			"album_id", album.ID, "err", err)
	}
}

// readEmbeddedArtwork extracts the raw bytes of the first embedded picture
// from an audio file using dhowden/tag.
func readEmbeddedArtwork(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	m, err := readTagFrom(f)
	if err != nil {
		return nil, err
	}

	pic := m.Picture()
	if pic == nil {
		return nil, nil
	}
	return pic.Data, nil
}

// sortTitle produces a lowercased sort key, stripping leading articles.
func sortTitle(title string) string {
	lower := strings.ToLower(title)
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, article) {
			return strings.TrimPrefix(lower, article)
		}
	}
	return lower
}

// readTagFrom reads audio tags from an io.ReadSeeker.
// This is a thin wrapper around tag.ReadFrom to allow test stubbing.
var readTagFrom = func(r io.ReadSeeker) (tag.Metadata, error) {
	return tag.ReadFrom(r)
}

// decodeImageBytes decodes raw image bytes (JPEG or PNG) into an image.Image.
func decodeImageBytes(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}
