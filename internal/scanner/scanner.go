// Package scanner implements the recursive filesystem scanner and fsnotify watcher.
// It produces file metadata records and drives the TMDB metadata agent.
// ADR-011: file identity is SHA-256 hash. ADR-024: bounded concurrency.
package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
)

// validExtensions is the set of container formats the scanner recognises.
var validExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true,
	".mov": true, ".wmv": true, ".ts": true, ".m2ts": true,
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true,
}

// imageExtensions is the set of image formats recognised for photo libraries.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".heic": true, ".avif": true,
}

// yearRE matches a 4-digit year, optionally surrounded by parentheses or dots.
var yearRE = regexp.MustCompile(`[\.\s]\(?(\d{4})\)?`)

// MetadataAgent is called for newly discovered files to fetch external metadata.
type MetadataAgent interface {
	Enrich(ctx context.Context, item *media.Item, file *media.File) error
}

// MediaService is the subset of media.Service used by the scanner.
type MediaService interface {
	FindOrCreateItem(ctx context.Context, p media.CreateItemParams) (*media.Item, error)
	FindOrCreateHierarchyItem(ctx context.Context, p media.CreateItemParams) (*media.Item, error)
	CreateOrUpdateFile(ctx context.Context, p media.CreateFileParams) (*media.File, bool, error)
	GetFileByPath(ctx context.Context, path string) (*media.File, error)
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	UpdateItemMetadata(ctx context.Context, p media.UpdateItemMetadataParams) (*media.Item, error)
	MarkFileActive(ctx context.Context, id uuid.UUID) error
	MarkMissing(ctx context.Context, id uuid.UUID) error
	DeleteFile(ctx context.Context, id uuid.UUID) error
	SoftDeleteItemIfEmpty(ctx context.Context, id uuid.UUID) error
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.File, error)
	CleanupMissingFiles(ctx context.Context, libraryID uuid.UUID) error
	CleanupEmptyItems(ctx context.Context, libraryID uuid.UUID) error
	DedupeTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) (media.DedupeResult, error)
}

// ConcurrencyProvider lets the scanner read current concurrency limits from
// the hot-reloadable config (ADR-024).
type ConcurrencyProvider interface {
	ScanFileConcurrency() int
	ScanLibraryConcurrency() int
}

// Scanner performs recursive directory scans and maintains the media_files table.
type Scanner struct {
	media  MediaService
	agent  MetadataAgent
	conc   ConcurrencyProvider
	logger *slog.Logger
}

// New creates a Scanner.
func New(mediaSvc MediaService, agent MetadataAgent, conc ConcurrencyProvider,
	logger *slog.Logger) *Scanner {
	return &Scanner{
		media:  mediaSvc,
		agent:  agent,
		conc:   conc,
		logger: logger,
	}
}

// ScanResult summarises a completed scan pass.
type ScanResult struct {
	LibraryID uuid.UUID
	Found     int
	New       int
	Missing   int
	Duration  time.Duration
}

// ScanLibrary performs a full recursive scan of all scan_paths for a library.
// libraryType is the media type ("movie", "show", "music", "photo") and is
// used when creating placeholder media_item records for newly discovered files.
// It respects the configured file concurrency limit (ADR-024).
func (s *Scanner) ScanLibrary(ctx context.Context, libraryID uuid.UUID, libraryType string, paths []string) (*ScanResult, error) {
	start := time.Now()
	result := &ScanResult{LibraryID: libraryID}

	s.logger.InfoContext(ctx, "scan starting",
		"library_id", libraryID,
		"library_type", libraryType,
		"paths", paths,
	)

	// Collect all files from all paths.
	var filePaths []string
	for _, root := range paths {
		if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				s.logger.WarnContext(ctx, "scan walk error", "path", path, "err", err)
				return nil // continue
			}
			if d.IsDir() {
				// Skip the .artwork directory tree (artwork storage, not media).
				if d.Name() == ".artwork" {
					return filepath.SkipDir
				}
				return nil
			}
			if !isMediaFile(path) {
				return nil
			}
			// Image files are only valid in photo libraries; skip them elsewhere
			// to avoid treating downloaded artwork (poster.jpg, fanart.jpg) as items.
			if isImageFile(path) && libraryType != "photo" {
				return nil
			}
			if !s.isAllowedPath(path, paths) {
				s.logger.WarnContext(ctx, "path outside library root, skipping", "path", path)
				return nil
			}
			filePaths = append(filePaths, path)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("scan walk %s: %w", root, err)
		}
	}

	s.logger.InfoContext(ctx, "scan walk complete", "library_id", libraryID, "files_found", len(filePaths))

	if len(filePaths) == 0 {
		result.Duration = time.Since(start)
		return result, nil
	}

	// Process files concurrently using a semaphore (ADR-024).
	// We use sync.WaitGroup instead of errgroup so that one file's error
	// cannot cancel the context for all other files.
	fileConcurrency := s.conc.ScanFileConcurrency()
	if fileConcurrency <= 0 {
		fileConcurrency = 4
	}

	type enrichWork struct {
		item *media.Item
		file *media.File
	}

	var (
		wg          sync.WaitGroup
		sem         = make(chan struct{}, fileConcurrency)
		found       atomic.Int64
		newCount    atomic.Int64
		enrichMu    sync.Mutex
		enrichQueue []enrichWork
	)

	for _, path := range filePaths {
		path := path
		wg.Add(1)
		sem <- struct{}{} // acquire slot
		go func() {
			defer wg.Done()
			defer func() { <-sem }() // release slot
			defer func() {
				if r := recover(); r != nil {
					s.logger.ErrorContext(ctx, "scan goroutine panic",
						"path", path, "panic", r)
				}
			}()

			if ctx.Err() != nil {
				s.logger.WarnContext(ctx, "scan context cancelled, stopping")
				return
			}

			item, file, isNew, err := s.processFile(ctx, libraryID, libraryType, path, paths)
			if err != nil {
				s.logger.WarnContext(ctx, "file scan error", "path", path, "err", err)
				return
			}
			found.Add(1)
			if isNew {
				newCount.Add(1)
			}
			if item != nil && file != nil {
				if s.agent != nil && (isNew || itemNeedsEnrich(item) || s.parentNeedsEnrich(ctx, item)) {
					enrichMu.Lock()
					enrichQueue = append(enrichQueue, enrichWork{item, file})
					enrichMu.Unlock()
				}
			}
		}()
	}

	// Wait for all file I/O and DB work to finish before starting enrichment.
	// This decouples the fast scan phase (hash + probe + DB) from the slow
	// network phase (TMDB search + artwork download) so the scan "completes"
	// promptly even when metadata fetching is slow.
	wg.Wait()

	// Post-scan stale-file detection: any file the DB thinks is active but
	// wasn't encountered on disk (e.g. after a scan-path change) gets marked
	// missing so the frontend stops serving broken stream URLs.
	//
	// Safety: skip orphan detection when the walk found very few files
	// compared to what the DB expects. This prevents mass-marking files
	// missing when the media volume is briefly unmounted (e.g. during
	// container restarts). Threshold: walk must find at least 50% of
	// known active files, otherwise it's likely a mount failure.
	activeFiles, _ := s.media.ListActiveFilesForLibrary(ctx, libraryID)
	if len(activeFiles) == 0 || len(filePaths) >= len(activeFiles)/2 {
		s.markOrphanedFiles(ctx, libraryID, filePaths)
	} else {
		s.logger.WarnContext(ctx, "skipping orphan detection — walk found far fewer files than expected (possible mount issue)",
			"library_id", libraryID, "walked", len(filePaths), "db_active", len(activeFiles))
	}

	// Clean up stale missing files from prior scans and remove items with
	// no remaining active files. This catches files that were already
	// status='missing' and invisible to the active-only orphan check above.
	if err := s.media.CleanupMissingFiles(ctx, libraryID); err != nil {
		s.logger.WarnContext(ctx, "cleanup missing files failed", "library_id", libraryID, "err", err)
	}
	if err := s.media.CleanupEmptyItems(ctx, libraryID); err != nil {
		s.logger.WarnContext(ctx, "cleanup empty items failed", "library_id", libraryID, "err", err)
	}

	if s.agent != nil && len(enrichQueue) > 0 {
		enrichConc := fileConcurrency
		if enrichConc > 4 {
			enrichConc = 4
		}
		enrichSem := make(chan struct{}, enrichConc)
		var enrichWg sync.WaitGroup
		for _, work := range enrichQueue {
			work := work
			enrichWg.Add(1)
			enrichSem <- struct{}{}
			go func() {
				defer enrichWg.Done()
				defer func() { <-enrichSem }()
				if err := s.agent.Enrich(ctx, work.item, work.file); err != nil {
					s.logger.WarnContext(ctx, "metadata enrich failed",
						"item_id", work.item.ID, "err", err)
				}
			}()
		}
		enrichWg.Wait()
	}

	if libraryType == "show" || libraryType == "movie" {
		if dedup, err := s.media.DedupeTopLevelItems(ctx, libraryType, &libraryID); err != nil {
			s.logger.WarnContext(ctx, "post-scan dedupe failed", "library_id", libraryID, "err", err)
		} else if dedup.MergedItems > 0 || dedup.ReparentedRows > 0 {
			s.logger.InfoContext(ctx, "post-scan dedupe merged duplicates",
				"library_id", libraryID,
				"merged_items", dedup.MergedItems,
				"merged_seasons", dedup.MergedSeasons,
				"merged_episodes", dedup.MergedEpisodes,
				"reparented_rows", dedup.ReparentedRows,
			)
		}
	}

	result.Found = int(found.Load())
	result.New = int(newCount.Load())
	result.Duration = time.Since(start)
	s.logger.InfoContext(ctx, "scan completed",
		"library_id", libraryID,
		"found", result.Found,
		"new", result.New,
		"duration_ms", result.Duration.Milliseconds(),
	)
	return result, nil
}

// processFile probes a single file and upserts its media_files record.
// Returns the item, file, whether the file is newly discovered, and any error.
// Enrichment is NOT run here — callers collect the returned item+file and
// run enrichment after all file I/O is done (see ScanLibrary).
func (s *Scanner) processFile(ctx context.Context, libraryID uuid.UUID, libraryType string, path string, roots []string) (*media.Item, *media.File, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, false, fmt.Errorf("stat %s: %w", path, err)
	}

	hash, err := HashFile(ctx, path, info)
	if err != nil {
		s.logger.WarnContext(ctx, "hash failed, proceeding without", "path", path, "err", err)
	}

	// Fast path: if the file is already known, the hash hasn't changed,
	// and we already have probe metadata (duration_ms), skip ffprobe
	// entirely — just mark the file active and return.
	// We still load the parent item to check whether it needs enrichment
	// (e.g. a file scanned before the TMDB key was configured).
	// Photos and music skip the fast path: photos need per-file items,
	// music needs album art and track duration propagated to items.
	if hash != nil && libraryType != "photo" && libraryType != "music" {
		if existing, err := s.media.GetFileByPath(ctx, path); err == nil &&
			existing.FileHash != nil && *existing.FileHash == *hash &&
			(existing.DurationMS != nil || isImageFile(path)) &&
			existing.Status != "deleted" {
			if err := s.media.MarkFileActive(ctx, existing.ID); err != nil {
				s.logger.WarnContext(ctx, "mark file active failed", "path", path, "err", err)
			}
			if item, err := s.media.GetItem(ctx, existing.MediaItemID); err == nil {
				if itemNeedsEnrich(item) || s.parentNeedsEnrich(ctx, item) {
					return item, existing, false, nil
				}
			}
			return nil, nil, false, nil
		}
	}

	var probe *ProbeResult
	if isImageFile(path) {
		probe = ProbeImage(path)
	} else {
		var probeErr error
		probe, probeErr = ProbeFile(ctx, path)
		if probeErr != nil {
			s.logger.WarnContext(ctx, "ffprobe failed, storing minimal metadata",
				"path", path, "err", probeErr)
			probe = &ProbeResult{}
		}
	}

	// Resolve or create the owning media item.
	// Music libraries use a hierarchy (artist -> album -> track) derived
	// from audio tags; TV show libraries use a hierarchy (show -> season ->
	// episode) derived from filename parsing; all other libraries use the
	// flat filename parser.
	var item *media.Item
	if libraryType == "music" && isMusicFile(path) {
		var musicErr error
		item, musicErr = s.processMusicHierarchy(ctx, libraryID, path, roots)
		if musicErr != nil {
			return nil, nil, false, fmt.Errorf("music hierarchy for %s: %w", path, musicErr)
		}
	} else if libraryType == "show" {
		var showErr error
		item, showErr = s.processShowHierarchy(ctx, libraryID, path)
		if showErr != nil {
			return nil, nil, false, fmt.Errorf("show hierarchy for %s: %w", path, showErr)
		}
	} else if libraryType == "photo" {
		// Photos: each file is its own item. Use the raw filename stem
		// (no year extraction) to avoid title-based deduplication collapsing
		// IMG_2024.jpg and IMG_2025.jpg into the same item.
		stem := filepath.Base(path)
		stem = stem[:len(stem)-len(filepath.Ext(stem))]
		title := strings.ReplaceAll(strings.ReplaceAll(stem, "_", " "), ".", " ")
		if title == "" {
			title = "Photo"
		}
		var createErr error
		item, createErr = s.media.FindOrCreateItem(ctx, media.CreateItemParams{
			LibraryID: libraryID,
			Type:      "photo",
			Title:     title,
			SortTitle: stem, // keep original stem for natural sort order
		})
		if createErr != nil {
			return nil, nil, false, fmt.Errorf("find or create item for %s: %w", path, createErr)
		}
	} else {
		title, year := parseFilename(path)
		itemType := fileTypeForLibrary(libraryType)
		var createErr error
		item, createErr = s.media.FindOrCreateItem(ctx, media.CreateItemParams{
			LibraryID: libraryID,
			Type:      itemType,
			Title:     title,
			SortTitle: title,
			Year:      year,
		})
		if createErr != nil {
			return nil, nil, false, fmt.Errorf("find or create item for %s: %w", path, createErr)
		}
	}

	p := media.CreateFileParams{
		MediaItemID:    item.ID,
		FilePath:       path,
		FileSize:       info.Size(),
		Container:      probe.Container,
		VideoCodec:     probe.VideoCodec,
		AudioCodec:     probe.AudioCodec,
		ResolutionW:    probe.ResolutionW,
		ResolutionH:    probe.ResolutionH,
		Bitrate:        probe.Bitrate,
		HDRType:        probe.HDRType,
		FrameRate:      probe.FrameRate,
		AudioStreams:    probe.AudioStreams,
		SubtitleStreams: probe.SubtitleStreams,
		Chapters:       probe.Chapters,
		FileHash:       hash,
		DurationMS:     probe.DurationMs,
	}

	file, isNew, err := s.media.CreateOrUpdateFile(ctx, p)
	if err != nil {
		return nil, nil, false, fmt.Errorf("upsert file: %w", err)
	}

	// Item-level duration_ms is set by TMDB enrichment or the progress endpoint.
	// File-level duration_ms (set above via CreateOrUpdateFile) is the authoritative
	// source for the player — no need to copy it to the item here.

	// Tracks: copy duration from the probe result to the item so the
	// children API can return it without joining against media_files.
	if libraryType == "music" && item.Type == "track" && probe.DurationMs != nil &&
		(item.DurationMS == nil || *item.DurationMS != *probe.DurationMs) {
		s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
			ID:         item.ID,
			Title:      item.Title,
			SortTitle:  item.SortTitle,
			DurationMS: probe.DurationMs,
		})
	}

	// Photos use the file itself as the poster. Set poster_path to the
	// relative path from the library root so /artwork/* can resolve it.
	if libraryType == "photo" && item.PosterPath == nil {
		for _, root := range roots {
			if rel, relErr := filepath.Rel(root, path); relErr == nil && !strings.HasPrefix(rel, "..") {
				relSlash := filepath.ToSlash(rel)
				s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
					ID:         item.ID,
					Title:      item.Title,
					SortTitle:  item.SortTitle,
					PosterPath: &relSlash,
				})
				break
			}
		}
	}

	return item, file, isNew, nil
}

// processShowHierarchy builds the show->season->episode hierarchy for a TV file.
// It parses the filename for show title, season number, and episode number,
// then finds or creates each level of the hierarchy.
// If parsing fails, it falls back to creating a flat episode item.
func (s *Scanner) processShowHierarchy(ctx context.Context, libraryID uuid.UUID, path string) (*media.Item, error) {
	showTitle, seasonNum, episodeNum, ok := ParseTVFilename(path)
	if !ok {
		// Could not parse S##E## — fall back to flat episode.
		title, year := parseFilename(path)
		return s.media.FindOrCreateItem(ctx, media.CreateItemParams{
			LibraryID: libraryID,
			Type:      "episode",
			Title:     title,
			SortTitle: title,
			Year:      year,
		})
	}

	// 1. Find or create the "show" item (parent_id=null).
	show, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "show",
		Title:     showTitle,
		SortTitle: showTitle,
	})
	if err != nil {
		return nil, fmt.Errorf("find or create show %q: %w", showTitle, err)
	}

	// 2. Find or create the "season" item (parent_id=show.ID, index=seasonNum).
	seasonTitle := fmt.Sprintf("Season %d", seasonNum)
	season, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "season",
		Title:     seasonTitle,
		SortTitle: seasonTitle,
		ParentID:  &show.ID,
		Index:     &seasonNum,
	})
	if err != nil {
		return nil, fmt.Errorf("find or create season %d for show %q: %w", seasonNum, showTitle, err)
	}

	// 3. Find or create the "episode" item (parent_id=season.ID, index=episodeNum).
	episodeTitle := fmt.Sprintf("Episode %d", episodeNum)
	episode, err := s.media.FindOrCreateHierarchyItem(ctx, media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "episode",
		Title:     episodeTitle,
		SortTitle: episodeTitle,
		ParentID:  &season.ID,
		Index:     &episodeNum,
	})
	if err != nil {
		return nil, fmt.Errorf("find or create episode %d for season %d of %q: %w",
			episodeNum, seasonNum, showTitle, err)
	}

	return episode, nil
}

// isAllowedPath validates that a path is under one of the library roots.
// Prevents path traversal (OWASP A01).
func (s *Scanner) isAllowedPath(path string, roots []string) bool {
	clean := filepath.Clean(path)
	for _, root := range roots {
		cleanRoot := filepath.Clean(root) + string(os.PathSeparator)
		if strings.HasPrefix(clean, cleanRoot) || clean == filepath.Clean(root) {
			return true
		}
	}
	return false
}

// numericNameRE matches filenames that are purely digits (e.g. Blu-ray stream "00000").
var numericNameRE = regexp.MustCompile(`^\d+$`)

// parseFilename extracts a human-readable title and optional year from a media
// file path. It handles common Kodi/Sonarr/scene naming conventions:
//   - "Movie Title (2008).mkv"
//   - "Movie.Title.2008.mkv"
//   - "Movie_Title_2008_1080p_BluRay.mkv"  (scene/torrent names)
//
// For Blu-ray rips where the filename is purely numeric (e.g. "00000.m2ts"),
// it falls back to the parent directory name (e.g. "War Machine (2026)").
func parseFilename(path string) (string, *int) {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// If the filename is purely numeric (Blu-ray stream index), use the parent dir.
	if numericNameRE.MatchString(stem) {
		dir := filepath.Base(filepath.Dir(path))
		if dir != "" && dir != "." && dir != string(os.PathSeparator) {
			return cleanTitle(dir)
		}
	}

	return cleanTitle(stem)
}

// cleanTitle normalises a raw media name (filename stem or stored title) into
// a human-readable title and optional year. It handles dot-separated,
// underscore-separated, and mixed naming, and strips everything after the year
// (resolution tags, source tags, group names, etc.).
func cleanTitle(name string) (title string, year *int) {
	// Normalise all common separators to dots so the year regex and
	// dot→space replacement work uniformly.
	name = strings.ReplaceAll(name, "_", ".")
	name = strings.ReplaceAll(name, " ", ".")

	if m := yearRE.FindStringSubmatchIndex(name); m != nil {
		yearStr := name[m[2]:m[3]]
		if y, err := strconv.Atoi(yearStr); err == nil && y >= 1888 && y <= 2100 {
			yr := y
			year = &yr
			name = name[:m[0]] // drop year + everything after (quality tags, group, etc.)
		}
	}

	title = strings.ReplaceAll(name, ".", " ")
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Unknown"
	}
	return title, year
}

// fileTypeForLibrary maps library type to the media_item type used for top-level items.
func fileTypeForLibrary(libraryType string) string {
	switch libraryType {
	case "show":
		return "episode"
	case "music":
		return "track"
	case "photo":
		return "photo"
	default:
		return "movie"
	}
}

// musicExtensions is the set of file extensions that are audio-only.
var musicExtensions = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true,
}

func isMediaFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return validExtensions[ext] || imageExtensions[ext]
}

func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext]
}

func isMusicFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return musicExtensions[ext]
}

// badPosterPath reports whether a stored poster_path is stale and must be
// re-fetched. A path is stale if it is absolute, contains ".." traversal,
// or uses the legacy .artwork/ directory prefix.
func badPosterPath(p string) bool {
	slashed := filepath.ToSlash(p)
	if filepath.IsAbs(p) || strings.HasPrefix(slashed, "/") || strings.HasPrefix(slashed, "../") {
		return true
	}
	// Legacy paths that used the .artwork/ directory need re-download.
	if strings.HasPrefix(slashed, ".artwork/") {
		return true
	}
	return false
}

// itemNeedsEnrich reports whether a media item still needs metadata enrichment.
// Episodes use ThumbPath (not PosterPath), so checking only PosterPath would
// cause episodes to be re-enriched on every scan.
func itemNeedsEnrich(item *media.Item) bool {
	switch item.Type {
	case "photo":
		return false
	case "episode":
		if item.ThumbPath != nil {
			return badPosterPath(*item.ThumbPath)
		}
		// No thumb yet — but if the item already has a summary it was enriched
		// successfully (just no artwork available). Don't re-enrich.
		return item.Summary == nil
	default:
		if item.PosterPath == nil {
			return true
		}
		if badPosterPath(*item.PosterPath) {
			return true
		}
		// Movies and shows need a content rating for parental filters.
		if (item.Type == "movie" || item.Type == "show") && item.ContentRating == nil {
			return true
		}
		return false
	}
}

// parentNeedsEnrich walks up the parent chain and returns true if any ancestor
// is missing a poster. This ensures episodes trigger re-enrichment of their
// parent show/season when artwork is absent.
func (s *Scanner) parentNeedsEnrich(ctx context.Context, item *media.Item) bool {
	switch item.Type {
	case "episode":
		if item.ParentID == nil {
			return false
		}
		// Check season.
		season, err := s.media.GetItem(ctx, *item.ParentID)
		if err != nil || season == nil {
			return false
		}
		if season.PosterPath == nil {
			return true
		}
		// Check show.
		if season.ParentID == nil {
			return false
		}
		show, err := s.media.GetItem(ctx, *season.ParentID)
		if err != nil || show == nil {
			return false
		}
		return show.PosterPath == nil

	case "track":
		if item.ParentID == nil {
			return false
		}
		// Check album.
		album, err := s.media.GetItem(ctx, *item.ParentID)
		if err != nil || album == nil {
			return false
		}
		if album.PosterPath == nil {
			return true
		}
		// Check artist.
		if album.ParentID == nil {
			return false
		}
		artist, err := s.media.GetItem(ctx, *album.ParentID)
		if err != nil || artist == nil {
			return false
		}
		return artist.PosterPath == nil
	}
	return false
}

// markOrphanedFiles deletes any DB-active files for this library that were not
// seen during the current scan pass, and soft-deletes parent items that have no
// remaining files. This handles scan-path changes and removed media.
func (s *Scanner) markOrphanedFiles(ctx context.Context, libraryID uuid.UUID, seenPaths []string) {
	seen := make(map[string]struct{}, len(seenPaths))
	for _, p := range seenPaths {
		seen[p] = struct{}{}
	}

	active, err := s.media.ListActiveFilesForLibrary(ctx, libraryID)
	if err != nil {
		s.logger.WarnContext(ctx, "list active files for orphan check failed",
			"library_id", libraryID, "err", err)
		return
	}

	affected := map[uuid.UUID]struct{}{}
	for _, f := range active {
		if _, ok := seen[f.FilePath]; !ok {
			if err := s.media.DeleteFile(ctx, f.ID); err != nil {
				s.logger.WarnContext(ctx, "delete orphaned file",
					"file_id", f.ID, "path", f.FilePath, "err", err)
			} else {
				s.logger.InfoContext(ctx, "deleted orphaned file",
					"file_id", f.ID, "path", f.FilePath)
				affected[f.MediaItemID] = struct{}{}
			}
		}
	}

	// Cascade: soft-delete parent items with no remaining files.
	for itemID := range affected {
		if err := s.media.SoftDeleteItemIfEmpty(ctx, itemID); err != nil {
			s.logger.WarnContext(ctx, "soft-delete empty item after orphan removal",
				"item_id", itemID, "err", err)
		}
	}
}
