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
// "The Black Eyed Peas, CL", "Beyonce / Jay-Z", or
// "Bo Diddley - Muddy Waters - Little Walter". Accepts comma, slash, "&",
// "and", "feat[.]?", "ft[.]?", "featuring", "with", and " - " (whitespace-
// bounded hyphen, so single-name hyphens like Jay-Z / Wu-Tang are
// unaffected). Stripping is applied only when the resulting primary
// already exists as a standalone artist in the library — otherwise
// legitimate band names like "Simon & Garfunkel" or "Nick Cave and the
// Bad Seeds" would be damaged.
var collabArtistRE = regexp.MustCompile(`(?i)(\s*,\s*|\s*/\s*|\s+(?:&|and|feat\.?|ft\.?|featuring|with)\s+|\s+-\s+).+$`)

// collabSecondaryRE captures the LAST name in a collab tag (everything
// after the final separator). Used as a fallback when the left-side
// primary doesn't match a library row but the right-side might —
// "X & Famous" rolls into Famous when X is a one-off feature.
var collabSecondaryRE = regexp.MustCompile(`(?i)^.+(\s*,\s*|\s*/\s*|\s+(?:&|and|feat\.?|ft\.?|featuring|with)\s+|\s+-\s+)`)

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

// secondaryArtistName returns the LAST collaborator (everything after the
// final separator). Returns "" if no collab marker is found. Used by
// resolveArtistTitle as a fallback when the leading primary isn't in the
// library — fixes "Glen Campbell & Elton John" type rows where the famous
// guest is on the right.
func secondaryArtistName(s string) string {
	trimmed := collabSecondaryRE.ReplaceAllString(s, "")
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
// Three corrections are applied in order:
//  1. "Last, First" → "First Last" (e.g. "Dylan, Bob" → "Bob Dylan"), so
//     taggers that write surname-first don't shard the catalog.
//  2. Collab tag → LEFT primary, if it exists as a standalone artist
//     ("Elton John & Bonnie Raitt" → "Elton John").
//  3. Collab tag → RIGHT secondary, if the right-side name exists as a
//     standalone artist and the left didn't match. Catches "X & Famous"
//     rows where the canonical the library knows about is the guest.
//
// If none apply, the tag is returned unchanged so legitimate multi-name
// bands like "Simon & Garfunkel" are preserved.
func (s *Scanner) resolveArtistTitle(ctx context.Context, libraryID uuid.UUID, tagArtist string) string {
	if flipped := flipLastFirst(tagArtist); flipped != "" {
		tagArtist = flipped
	}
	if primary := primaryArtistName(tagArtist); primary != "" {
		if existing, err := s.media.FindTopLevelItem(ctx, libraryID, "artist", primary); err == nil && existing != nil {
			return existing.Title
		}
	}
	if secondary := secondaryArtistName(tagArtist); secondary != "" {
		if existing, err := s.media.FindTopLevelItem(ctx, libraryID, "artist", secondary); err == nil && existing != nil {
			return existing.Title
		}
	}
	return tagArtist
}

// processMusicHierarchy reads tags from a music file and ensures the
// AlbumArtist -> Album -> Track hierarchy exists. Returns the track item and
// the parsed tags (so the caller can thread ReplayGain and other per-file
// audio-quality tags into the media_file insert).
//
// AlbumArtist is the binding key for the artist level — Artist changes
// per-track on compilations and classical recordings, but AlbumArtist is
// stable for the album. Picard-tagged libraries rely on this behaviour.
func (s *Scanner) processMusicHierarchy(ctx context.Context, libraryID uuid.UUID, path string, roots []string) (*media.Item, *MusicTags, error) {
	tags, err := ReadMusicTags(path)
	if err != nil {
		return nil, nil, err
	}

	// 1. Find or create the artist (top-level, parent_id=null). Use the
	//    album artist so compilations group under one parent rather than
	//    fanning out to one artist per track.
	artistTitle := s.resolveArtistTitle(ctx, libraryID, tags.AlbumArtist)
	artistParams := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "artist",
		Title:     artistTitle,
		SortTitle: sortTitle(artistTitle),
	}
	if tags.MBAlbumArtistID != uuid.Nil {
		artistParams.MusicBrainzID = &tags.MBAlbumArtistID
		artistParams.MusicBrainzArtistID = &tags.MBAlbumArtistID
	} else if tags.MBArtistID != uuid.Nil {
		artistParams.MusicBrainzID = &tags.MBArtistID
		artistParams.MusicBrainzArtistID = &tags.MBArtistID
	}
	artist, err := s.media.FindOrCreateHierarchyItem(ctx, artistParams)
	if err != nil {
		return nil, nil, err
	}

	// 2. Find or create the album (parent_id=artist.id).
	// When tags.Album is empty (untagged file) the album row would land
	// with title="" — multiple such files under one artist all collide
	// into the same nameless album, and the UI shows a row with no
	// title. Fall back to deriving a title from the file path: the
	// parent folder's basename is almost always the album folder name
	// for any sanely-organized library, and even on flat layouts where
	// the parent folder equals the artist's name, grouping still works
	// because every same-artist file lands in one bucket.
	albumTitle := albumTitleOrFallback(tags.Album, path, artistTitle)
	var albumYear *int
	if tags.Year != 0 {
		albumYear = &tags.Year
	}
	var origYear *int
	if tags.OriginalYear != 0 {
		origYear = &tags.OriginalYear
	}
	var discTotal *int
	if tags.DiscTotal > 0 {
		discTotal = &tags.DiscTotal
	}
	var trackTotal *int
	if tags.TrackTotal > 0 {
		trackTotal = &tags.TrackTotal
	}
	albumParams := media.CreateItemParams{
		LibraryID:    libraryID,
		Type:         "album",
		Title:        albumTitle,
		SortTitle:    sortTitle(albumTitle),
		Year:         albumYear,
		OriginalYear: origYear,
		ParentID:     &artist.ID,
		Genres:       tags.Genres,
		Compilation:  tags.Compilation,
		ReleaseType:  tags.ReleaseType,
		DiscTotal:    discTotal,
		TrackTotal:   trackTotal,
	}
	if tags.MBReleaseID != uuid.Nil {
		albumParams.MusicBrainzID = &tags.MBReleaseID
		albumParams.MusicBrainzReleaseID = &tags.MBReleaseID
	}
	if tags.MBReleaseGroupID != uuid.Nil {
		albumParams.MusicBrainzReleaseGroupID = &tags.MBReleaseGroupID
	}
	if tags.MBAlbumArtistID != uuid.Nil {
		albumParams.MusicBrainzAlbumArtistID = &tags.MBAlbumArtistID
	}
	album, err := s.media.FindOrCreateHierarchyItem(ctx, albumParams)
	if err != nil {
		return nil, nil, err
	}

	// 3. Find or create the track (parent_id=album.id, index=track number).
	var trackIndex *int
	if tags.Track > 0 {
		trackIndex = &tags.Track
	}
	trackParams := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "track",
		Title:     tags.Title,
		SortTitle: sortTitle(tags.Title),
		Year:      albumYear,
		ParentID:  &album.ID,
		Index:     trackIndex,
		Genres:    tags.Genres,
	}
	if tags.MBRecordingID != uuid.Nil {
		trackParams.MusicBrainzID = &tags.MBRecordingID
	}
	if tags.MBReleaseID != uuid.Nil {
		trackParams.MusicBrainzReleaseID = &tags.MBReleaseID
	}
	if tags.MBReleaseGroupID != uuid.Nil {
		trackParams.MusicBrainzReleaseGroupID = &tags.MBReleaseGroupID
	}
	if tags.MBArtistID != uuid.Nil {
		trackParams.MusicBrainzArtistID = &tags.MBArtistID
	}
	if tags.MBAlbumArtistID != uuid.Nil {
		trackParams.MusicBrainzAlbumArtistID = &tags.MBAlbumArtistID
	}
	track, err := s.media.FindOrCreateHierarchyItem(ctx, trackParams)
	if err != nil {
		return nil, nil, err
	}

	// 4. Album art: prefer disk-side cover files (cover.jpg / folder.jpg /
	// album.jpg / front.jpg / poster.jpg), then fall back to embedded
	// tags. Plex and Jellyfin both do this — lots of ripped libraries
	// keep cover art as loose files in the album folder rather than
	// re-embedding it into every track, so an embedded-only reader
	// misses most of what's actually on disk.
	s.extractAlbumArt(ctx, album, path, roots, tags.AlbumArt)

	// 5. Artist art: look for artist.jpg / poster.jpg / folder.jpg in the
	// artist directory (one level up from the album). Skipped when the
	// artist already has a poster — an enricher pass (TheAudioDB) or
	// a prior disk-discovered file would have set it, and we don't
	// want to overwrite a curated image on every track re-scan.
	s.extractArtistArt(ctx, artist, path, roots)

	return track, tags, nil
}

// albumArtFilenames lists the on-disk cover-art filenames we check (in
// order) in an album's directory. Matches Plex + Jellyfin + MusicBrainz
// Picard's defaults. Checked case-insensitively against the real
// directory listing so "Cover.JPG" and "Folder.jpeg" still match.
var albumArtFilenames = []string{
	"cover.jpg", "cover.jpeg", "cover.png",
	"folder.jpg", "folder.jpeg", "folder.png",
	"album.jpg", "album.jpeg", "album.png",
	"front.jpg", "front.jpeg", "front.png",
	"poster.jpg", "poster.jpeg", "poster.png",
}

// artistArtFilenames lists the on-disk artist-portrait filenames we
// check in the artist directory. "artist.jpg" is unambiguous; the
// "folder.jpg"/"poster.jpg" entries only matter when the artist
// directory is distinct from the album directory (nested layout) —
// on flat layouts they're the album's own art and we skip them in
// extractArtistArt.
var artistArtFilenames = []string{
	"artist.jpg", "artist.jpeg", "artist.png",
	"poster.jpg", "poster.jpeg", "poster.png",
	"folder.jpg", "folder.jpeg", "folder.png",
}

// extractAlbumArt writes the album's poster to {album.id}-poster.jpg in
// the album directory. Source order: on-disk cover files first
// (cover.jpg / folder.jpg / front.jpg / poster.jpg / album.jpg),
// then the track's embedded picture tag when hasEmbedded is true.
// Returns the relative poster path, or "" when no source is available.
//
// The filename is qualified by album ID so that flat libraries — where
// every album under an artist keeps its tracks directly in the artist
// folder — don't overwrite each other's poster. Using an unqualified
// "poster.jpg" here was the cause of "every album has the same art" on
// flat layouts: whichever album scanned last won the filename, and the
// DB then pointed every album at that one file.
func (s *Scanner) extractAlbumArt(ctx context.Context, album *media.Item, filePath string, roots []string, hasEmbedded bool) string {
	absDir := filepath.Dir(filePath)

	var artData []byte
	if data, ok := findArtOnDisk(absDir, albumArtFilenames); ok {
		artData = data
	} else if hasEmbedded {
		data, err := readEmbeddedArtwork(filePath)
		if err == nil && len(data) > 0 {
			artData = data
		}
	}
	if len(artData) == 0 {
		return ""
	}

	// Store the poster in the same directory as the music file, keyed by
	// the album's UUID to avoid cross-album collisions in flat layouts.
	posterFile := filepath.Join(absDir, album.ID.String()+"-poster.jpg")

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
		// Fallback: use album-dir/{id}-poster.jpg (may not resolve correctly).
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(absDir), album.ID.String()+"-poster.jpg"))
	}

	// If the ID-qualified poster already exists on disk, just ensure
	// the DB path is correct.
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

// extractArtistArt copies a disk-side artist portrait into the artist's
// directory as {artist.id}-poster.jpg and updates the artist's
// poster_path. The lookup directory is the PARENT of the album
// directory — i.e. the artist folder in a nested layout. On a flat
// layout where the artist folder IS the album folder, the lookup
// still runs but only matches "artist.jpg" (the unambiguous one);
// "folder.jpg" / "poster.jpg" there belong to the album and are
// passed over.
//
// Skipped when the artist already has a poster_path — that's either
// a previously-discovered disk file or TheAudioDB enricher output,
// and we don't want to re-copy on every track rescan or overwrite
// a curated portrait.
func (s *Scanner) extractArtistArt(ctx context.Context, artist *media.Item, filePath string, roots []string) string {
	// Skip when the artist already has a valid poster on disk. If
	// poster_path is set but the file is missing (e.g. a prior
	// TheAudioDB match didn't actually land bytes on disk), treat
	// it as unset so this scan gets a chance to find a local cover
	// and self-heal the broken reference.
	if artist.PosterPath != nil && *artist.PosterPath != "" {
		if resolveArtworkPath(*artist.PosterPath, roots) != "" {
			return ""
		}
	}

	albumDir := filepath.Dir(filePath)
	artistDir := filepath.Dir(albumDir)

	// On flat layouts the artist directory is the same as the album
	// directory, so cover.jpg / folder.jpg / poster.jpg there is
	// album art — not artist art. Restrict the candidates to the
	// unambiguous "artist.jpg" to avoid stealing the album's cover.
	candidates := artistArtFilenames
	if artistDir == albumDir {
		candidates = []string{"artist.jpg", "artist.jpeg", "artist.png"}
	}

	artData, ok := findArtOnDisk(artistDir, candidates)
	if !ok {
		return ""
	}

	posterFile := filepath.Join(artistDir, artist.ID.String()+"-poster.jpg")

	relPath := ""
	for _, root := range roots {
		if rel, err := filepath.Rel(root, posterFile); err == nil && !strings.HasPrefix(rel, "..") {
			relPath = filepath.ToSlash(rel)
			break
		}
	}
	if relPath == "" {
		relPath = filepath.ToSlash(filepath.Join(filepath.Base(artistDir), artist.ID.String()+"-poster.jpg"))
	}

	// If an ID-qualified portrait already exists on disk (left over
	// from a prior scan), just make sure the DB is pointing at it.
	if _, err := os.Stat(posterFile); err == nil {
		if artist.PosterPath == nil || *artist.PosterPath != relPath {
			s.updateArtistPoster(ctx, artist, relPath)
		}
		return relPath
	}

	var outData []byte
	if img, imgErr := decodeImageBytes(artData); imgErr == nil {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err == nil {
			outData = buf.Bytes()
		}
	}
	if outData == nil {
		outData = artData
	}

	if err := os.WriteFile(posterFile, outData, 0o644); err != nil {
		s.logger.WarnContext(ctx, "failed to write artist art",
			"artist_id", artist.ID, "err", err)
		return ""
	}

	s.updateArtistPoster(ctx, artist, relPath)
	return relPath
}

// updateArtistPoster sets the artist's poster_path in the database.
func (s *Scanner) updateArtistPoster(ctx context.Context, artist *media.Item, relPath string) {
	relPath = filepath.ToSlash(relPath)
	if _, err := s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
		ID:         artist.ID,
		Title:      artist.Title,
		SortTitle:  artist.SortTitle,
		Year:       artist.Year,
		PosterPath: &relPath,
	}); err != nil {
		s.logger.WarnContext(ctx, "failed to update artist poster_path",
			"artist_id", artist.ID, "err", err)
	}
}

// resolveArtworkPath joins relPath against each root and returns the
// first absolute path that exists on disk, or "" when none do. Used
// to tell a valid poster_path (file present, nothing to do) from a
// stale one (file missing — a prior download failed but the DB still
// advertises the reference).
func resolveArtworkPath(relPath string, roots []string) string {
	if relPath == "" {
		return ""
	}
	for _, root := range roots {
		abs := filepath.Join(root, filepath.FromSlash(relPath))
		if info, err := os.Stat(abs); err == nil && !info.IsDir() && info.Size() > 0 {
			return abs
		}
	}
	return ""
}

// findArtOnDisk scans dir for any of the candidate filenames and
// returns the contents of the first hit. Matching is case-insensitive
// because ripped libraries are inconsistent — "Cover.jpg", "FOLDER.JPG",
// and "folder.JPEG" all coexist in the wild. A single ReadDir beats
// one os.Stat per candidate by an order of magnitude when the list
// has ~15 entries.
func findArtOnDisk(dir string, candidates []string) ([]byte, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}
	// Build a lowercased name → real DirEntry index so we can honor
	// the caller's candidate priority order while matching
	// case-insensitively.
	byLower := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		byLower[strings.ToLower(e.Name())] = e
	}
	for _, c := range candidates {
		e, ok := byLower[strings.ToLower(c)]
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(data) == 0 {
			continue
		}
		return data, true
	}
	return nil, false
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

// albumTitleOrFallback returns tagged when non-empty, otherwise derives
// a title from the file path. The parent directory's basename works for
// 99% of layouts: hierarchical "/Artist/Album/track.flac" produces the
// album name; flat "/Artist/track.flac" produces the artist name, which
// at least keeps every untagged file under one (artist-named) bucket
// instead of collapsing into a nameless album that collides with every
// other untagged album. Last-ditch "Untitled" if even that fails.
func albumTitleOrFallback(tagged, filePath, artistTitle string) string {
	if strings.TrimSpace(tagged) != "" {
		return tagged
	}
	parent := filepath.Base(filepath.Dir(filePath))
	if parent != "" && parent != "." && parent != string(filepath.Separator) {
		// Don't return the artist name verbatim — give the user a hint
		// that this row is filling in for a missing tag, while still
		// keeping the bucket distinct from other artists' untagged
		// albums.
		if strings.EqualFold(parent, artistTitle) {
			return "Untitled Album"
		}
		return parent
	}
	return "Untitled Album"
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
