// Package scanner implements the recursive filesystem scanner and fsnotify watcher.
// It produces file metadata records and drives the TMDB metadata agent.
// ADR-011: file identity is SHA-256 hash. ADR-024: bounded concurrency.
package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/onscreen/onscreen/internal/domain/media"
)

var tracer = otel.Tracer("onscreen/scanner")

// validExtensions is the set of container formats the scanner recognises.
var validExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true,
	".mov": true, ".wmv": true, ".ts": true, ".m2ts": true,
	// Lossy / common music + audiobooks (m4b is the standard
	// chaptered audiobook container)
	".mp3": true, ".m4a": true, ".m4b": true, ".aac": true, ".ogg": true, ".opus": true,
	// Lossless PCM (CD-quality + hi-res)
	".flac": true, ".wav": true, ".aif": true, ".aiff": true, ".alac": true,
	// Lossless compressed (non-FLAC)
	".wv": true, ".ape": true, ".tak": true,
	// DSD (direct stream digital — SACD rips, hi-res audiophile masters)
	".dsf": true, ".dff": true,
}

// imageExtensions is the set of image formats recognised for photo libraries.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".heic": true, ".avif": true,
}

// bookExtensions: CBZ today (Stage 1 of v2.1 books). CBR (RAR-based)
// and EPUB are deferred until we pick the parser deps. Kept separate
// from validExtensions so it can grow without polluting the
// movie/music detection paths.
var bookExtensions = map[string]bool{
	".cbz": true,
}

// yearRE matches a 4-digit year, optionally surrounded by parentheses, square
// brackets, or dots.
var yearRE = regexp.MustCompile(`[\.\s][\(\[]?(\d{4})[\)\]]?`)

// bracketPrefixRE matches a leading release-group prefix in square brackets
// like `[ToonsHub] My Hero Academia`, `[QWERTY] 8 Out Of 10 Cats`, or
// `[DKB] Sentai Daishikkaku`. The bracketed token is never part of the
// actual show title, but it gets pulled into the parsed title from the
// folder/filename and then poisons the TMDB search query (TMDB returns no
// match because it's looking for the literal `[ToonsHub] My Hero Academia`).
// Strip mirrors the existing pattern in `ListDuplicateTopLevelItems` /
// `MergeTopLevelDuplicates` SQL so the enrichment search and the dedup
// normalization agree on what counts as the canonical title.
var bracketPrefixRE = regexp.MustCompile(`(?i)^\s*\[[^\]]+\]\s*`)

// StripReleaseGroupPrefix removes a leading `[release-group]` token plus
// surrounding whitespace from a title. Exported so the admin
// "re-enrich-unmatched" tool can clean stored titles in bulk without
// pulling in the full cleanTitle pipeline (which is filename-shaped
// and applies year extraction). Called on already-stored titles where
// year/quality tags have long since been stripped.
func StripReleaseGroupPrefix(title string) string {
	return bracketPrefixRE.ReplaceAllString(title, "")
}

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
	UpdateItemLyrics(ctx context.Context, id uuid.UUID, plain, synced *string) error
	MarkFileActive(ctx context.Context, id uuid.UUID) error
	MarkMissing(ctx context.Context, id uuid.UUID) error
	DeleteFile(ctx context.Context, id uuid.UUID) error
	SoftDeleteItemIfEmpty(ctx context.Context, id uuid.UUID) error
	RestoreItemAncestry(ctx context.Context, id uuid.UUID) error
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.File, error)
	CleanupMissingFiles(ctx context.Context, libraryID uuid.UUID) error
	PurgeDeletedFiles(ctx context.Context, libraryID uuid.UUID) (int64, error)
	CleanupEmptyItems(ctx context.Context, libraryID uuid.UUID) error
	GetEnrichAttemptedAt(ctx context.Context, id uuid.UUID) (*time.Time, error)
	TouchEnrichAttempt(ctx context.Context, id uuid.UUID) error
	UpsertPhotoMetadata(ctx context.Context, p media.PhotoMetadataParams) error
	DedupeTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) (media.DedupeResult, error)
	DedupeChildItems(ctx context.Context, itemType string, parentID *uuid.UUID) (media.DedupeResult, error)
	MergeCollabArtists(ctx context.Context, libraryID *uuid.UUID) (media.DedupeResult, error)
	ListItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]media.Item, error)
	FindTopLevelItem(ctx context.Context, libraryID uuid.UUID, itemType, title string) (*media.Item, error)
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
	ctx, span := tracer.Start(ctx, "scanner.library", trace.WithAttributes(
		attribute.String("library.id", libraryID.String()),
		attribute.String("library.type", libraryType),
		attribute.Int("library.path_count", len(paths)),
	))
	defer span.End()

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
				if s.agent != nil && s.shouldEnrich(ctx, item, isNew) {
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
	if purged, err := s.media.PurgeDeletedFiles(ctx, libraryID); err != nil {
		s.logger.WarnContext(ctx, "purge deleted files failed", "library_id", libraryID, "err", err)
	} else if purged > 0 {
		s.logger.InfoContext(ctx, "purged soft-deleted file rows", "library_id", libraryID, "count", purged)
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
				if err := s.media.TouchEnrichAttempt(ctx, work.item.ID); err != nil {
					s.logger.WarnContext(ctx, "touch enrich attempt failed",
						"item_id", work.item.ID, "err", err)
				}
			}()
		}
		enrichWg.Wait()
	}

	s.dedupeLibrary(ctx, libraryID, libraryType)

	result.Found = int(found.Load())
	result.New = int(newCount.Load())
	result.Duration = time.Since(start)
	span.SetAttributes(
		attribute.Int("scan.files_found", result.Found),
		attribute.Int("scan.files_new", result.New),
	)
	s.logger.InfoContext(ctx, "scan completed",
		"library_id", libraryID,
		"found", result.Found,
		"new", result.New,
		"duration_ms", result.Duration.Milliseconds(),
	)
	return result, nil
}

// dedupeLibrary collapses duplicates for the scanned library. For show and
// movie libraries it merges top-level items. For music it merges artists,
// then walks each artist and merges duplicate albums within it (music tags
// are often inconsistent across a single release's tracks, producing variant
// album rows with names like "Abbey Road" vs "Abbey Road (Remastered)").
// Photos are flat so there's nothing to dedupe. Errors are logged and
// swallowed so a dedupe failure never fails the scan.
func (s *Scanner) dedupeLibrary(ctx context.Context, libraryID uuid.UUID, libraryType string) {
	switch libraryType {
	case "show", "movie":
		dedup, err := s.media.DedupeTopLevelItems(ctx, libraryType, &libraryID)
		if err != nil {
			s.logger.WarnContext(ctx, "post-scan dedupe failed", "library_id", libraryID, "err", err)
			return
		}
		if dedup.MergedItems > 0 || dedup.ReparentedRows > 0 {
			s.logger.InfoContext(ctx, "post-scan dedupe merged duplicates",
				"library_id", libraryID,
				"merged_items", dedup.MergedItems,
				"merged_seasons", dedup.MergedSeasons,
				"merged_episodes", dedup.MergedEpisodes,
				"reparented_rows", dedup.ReparentedRows,
			)
		}
	case "music":
		s.dedupeMusicLibrary(ctx, libraryID)
	}
}

// dedupeMusicLibrary merges duplicate artists at the top level, then merges
// duplicate albums under each surviving artist. Collab merge runs first so
// scan-order doesn't matter: "Elton John & Bonnie Raitt" scanned before
// "Elton John" still collapses into the canonical artist once both exist.
// Album dedupe runs last so album rows that reparent during artist merges
// are seen under the correct artist before their own dedupe pass.
func (s *Scanner) dedupeMusicLibrary(ctx context.Context, libraryID uuid.UUID) {
	collabDedup, err := s.media.MergeCollabArtists(ctx, &libraryID)
	if err != nil {
		s.logger.WarnContext(ctx, "collab artist merge failed", "library_id", libraryID, "err", err)
	} else if collabDedup.MergedItems > 0 || collabDedup.ReparentedRows > 0 {
		s.logger.InfoContext(ctx, "collab artist merge collapsed rows",
			"library_id", libraryID,
			"merged_items", collabDedup.MergedItems,
			"reparented_rows", collabDedup.ReparentedRows,
		)
	}

	artistDedup, err := s.media.DedupeTopLevelItems(ctx, "artist", &libraryID)
	if err != nil {
		s.logger.WarnContext(ctx, "artist dedupe failed", "library_id", libraryID, "err", err)
	} else if artistDedup.MergedItems > 0 || artistDedup.ReparentedRows > 0 {
		s.logger.InfoContext(ctx, "artist dedupe merged duplicates",
			"library_id", libraryID,
			"merged_items", artistDedup.MergedItems,
			"reparented_rows", artistDedup.ReparentedRows,
		)
	}

	var totalAlbumMerged, totalAlbumReparented int
	const pageSize int32 = 500
	for offset := int32(0); ; offset += pageSize {
		artists, err := s.media.ListItems(ctx, libraryID, "artist", pageSize, offset)
		if err != nil {
			s.logger.WarnContext(ctx, "list artists for album dedupe failed",
				"library_id", libraryID, "err", err)
			break
		}
		if len(artists) == 0 {
			break
		}
		for i := range artists {
			artistID := artists[i].ID
			albumDedup, err := s.media.DedupeChildItems(ctx, "album", &artistID)
			if err != nil {
				s.logger.WarnContext(ctx, "album dedupe failed",
					"artist_id", artistID, "err", err)
				continue
			}
			totalAlbumMerged += albumDedup.MergedItems
			totalAlbumReparented += albumDedup.ReparentedRows
		}
		if len(artists) < int(pageSize) {
			break
		}
	}
	if totalAlbumMerged > 0 || totalAlbumReparented > 0 {
		s.logger.InfoContext(ctx, "album dedupe merged duplicates",
			"library_id", libraryID,
			"merged_items", totalAlbumMerged,
			"reparented_rows", totalAlbumReparented,
		)
	}
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

	// Sticky tombstone: if a file row already exists with status='deleted',
	// the user explicitly deleted this content from the library (typically
	// to remove a duplicate or a bad metadata match). Bail out — even
	// though the file is still on disk, we don't auto-resurrect.
	//
	// Without this, the slow path below would call FindOrCreateHierarchyItem
	// for the show/season/episode (or FindOrCreateItem for a flat library),
	// the lookup queries filter `deleted_at IS NULL` so they MISS the
	// soft-deleted row and CREATE a fresh duplicate item with the same
	// title; then CreateOrUpdateFile reassigns the tombstoned file row to
	// the new item. Net effect from the user's perspective: deleted shows
	// reappear in Recently Added under a new ID. Recovery from a tombstone
	// requires an explicit restore action, not just leaving the file on disk.
	if existing, err := s.media.GetFileByPath(ctx, path); err == nil && existing.Status == "deleted" {
		return nil, nil, false, nil
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
			wasInactive := existing.Status != "active"
			if err := s.media.MarkFileActive(ctx, existing.ID); err != nil {
				s.logger.WarnContext(ctx, "mark file active failed", "path", path, "err", err)
			}
			if wasInactive {
				if err := s.media.RestoreItemAncestry(ctx, existing.MediaItemID); err != nil {
					s.logger.WarnContext(ctx, "restore ancestry failed", "path", path, "err", err)
				}
			}
			if item, err := s.media.GetItem(ctx, existing.MediaItemID); err == nil {
				if s.shouldEnrich(ctx, item, false) {
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
		if isMusicFile(path) {
			lossless := isLosslessAudio(path, probe.AudioCodec)
			probe.Lossless = &lossless
		}
	}

	// Resolve or create the owning media item.
	// Music libraries use a hierarchy (artist -> album -> track) derived
	// from audio tags; TV show libraries use a hierarchy (show -> season ->
	// episode) derived from filename parsing; all other libraries use the
	// flat filename parser.
	var item *media.Item
	var musicTags *MusicTags
	if libraryType == "music" && isMusicFile(path) {
		var musicErr error
		item, musicTags, musicErr = s.processMusicHierarchy(ctx, libraryID, path, roots)
		if musicErr != nil {
			return nil, nil, false, fmt.Errorf("music hierarchy for %s: %w", path, musicErr)
		}
	} else if libraryType == "music" && isVideoFile(path) {
		// Music videos share the music library with audio tracks.
		// Routed to a dedicated hierarchy so they hang off the artist
		// (no album), and the existing video transcode + player
		// pipeline handles playback.
		var mvErr error
		item, mvErr = s.processMusicVideo(ctx, libraryID, path, roots)
		if mvErr != nil {
			return nil, nil, false, fmt.Errorf("music video for %s: %w", path, mvErr)
		}
	} else if libraryType == "audiobook" && isAudiobookFile(path) {
		// Audiobooks: one file = one item (flat model for v2.0). The
		// m4b container's embedded chapter markers come through via
		// ffprobe at playback time; a richer author/series hierarchy
		// is planned for v2.1.
		var abErr error
		item, abErr = s.processAudiobook(ctx, libraryID, path, roots)
		if abErr != nil {
			return nil, nil, false, fmt.Errorf("audiobook for %s: %w", path, abErr)
		}
	} else if libraryType == "podcast" && isAudiobookFile(path) {
		// Podcasts: same audio file detection as audiobooks (mp3 + m4a
		// + others). Folder = show, file = episode. Subscriptions
		// (RSS auto-download) are v2.1.
		var pcErr error
		item, pcErr = s.processPodcast(ctx, libraryID, path, roots)
		if pcErr != nil {
			return nil, nil, false, fmt.Errorf("podcast for %s: %w", path, pcErr)
		}
	} else if libraryType == "home_video" && isVideoFile(path) {
		// Home videos: personal footage with no external metadata
		// agent. Date taken comes from file mtime → populates
		// originally_available_at so the library page can sort by
		// recording date (not scan date).
		var hvErr error
		item, hvErr = s.processHomeVideo(ctx, libraryID, path, info.ModTime())
		if hvErr != nil {
			return nil, nil, false, fmt.Errorf("home video for %s: %w", path, hvErr)
		}
	} else if libraryType == "book" && isBookFile(path) {
		// Books: CBZ in v2.1 Stage 1 (CBR + EPUB land later). One
		// file = one row, page count probed via archive/zip and
		// stashed on duration_ms ("duration" for a book = pages).
		var bkErr error
		item, bkErr = s.processBook(ctx, libraryID, path, roots)
		if bkErr != nil {
			return nil, nil, false, fmt.Errorf("book for %s: %w", path, bkErr)
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
		// Movies: Radarr's recommended layout is
		// `{Movie CleanTitle} ({Release Year})/file.mkv`, with optional
		// `{tmdb-NNN}` / `[imdb-tt...]` suffix on the folder. Parsing
		// those markers lets the same row collapse onto an existing
		// canonical match by id even when title parsing differs.
		movieParams := media.CreateItemParams{
			LibraryID: libraryID,
			Type:      itemType,
			Title:     title,
			SortTitle: title,
			Year:      year,
		}
		if itemType == "movie" {
			folderIDs := ParseFolderIDs(filepath.Base(filepath.Dir(path)))
			if folderIDs.TMDBID > 0 {
				t := folderIDs.TMDBID
				movieParams.TMDBID = &t
			}
			if folderIDs.IMDBID != "" {
				i := folderIDs.IMDBID
				movieParams.IMDBID = &i
			}
		}
		var createErr error
		item, createErr = s.media.FindOrCreateItem(ctx, movieParams)
		if createErr != nil {
			return nil, nil, false, fmt.Errorf("find or create item for %s: %w", path, createErr)
		}
	}

	p := media.CreateFileParams{
		MediaItemID:     item.ID,
		FilePath:        path,
		FileSize:        info.Size(),
		Container:       probe.Container,
		VideoCodec:      probe.VideoCodec,
		AudioCodec:      probe.AudioCodec,
		ResolutionW:     probe.ResolutionW,
		ResolutionH:     probe.ResolutionH,
		Bitrate:         probe.Bitrate,
		HDRType:         probe.HDRType,
		FrameRate:       probe.FrameRate,
		AudioStreams:    probe.AudioStreams,
		SubtitleStreams: probe.SubtitleStreams,
		Chapters:        probe.Chapters,
		FileHash:        hash,
		DurationMS:      probe.DurationMs,
		BitDepth:        probe.BitDepth,
		SampleRate:      probe.SampleRate,
		ChannelLayout:   probe.ChannelLayout,
		Lossless:        probe.Lossless,
	}
	if musicTags != nil {
		p.ReplayGainTrackGain = musicTags.ReplayGainTrackGain
		p.ReplayGainTrackPeak = musicTags.ReplayGainTrackPeak
		p.ReplayGainAlbumGain = musicTags.ReplayGainAlbumGain
		p.ReplayGainAlbumPeak = musicTags.ReplayGainAlbumPeak
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

	// Persist EXIF for photos. Done after the item exists so the row keys onto
	// item.ID. Failures are non-fatal — a missing EXIF block is normal for
	// PNG/screenshots and shouldn't fail the scan.
	if libraryType == "photo" && isImageFile(path) {
		s.persistPhotoEXIF(ctx, item, path)
	}

	return item, file, isNew, nil
}

// persistPhotoEXIF extracts EXIF tags from an image and writes them to
// photo_metadata. Also bumps the parent item's originally_available_at when
// EXIF carries a DateTimeOriginal — that field already drives date sorting on
// other media types, so photos slot in for free.
func (s *Scanner) persistPhotoEXIF(ctx context.Context, item *media.Item, path string) {
	ex, err := ExtractEXIF(path)
	if err != nil {
		s.logger.WarnContext(ctx, "exif extract failed", "path", path, "err", err)
		return
	}
	if ex == nil {
		return // no EXIF block — common for PNG/GIF
	}

	var rawJSON []byte
	if ex.Raw != nil {
		if b, mErr := json.Marshal(ex.Raw); mErr == nil {
			rawJSON = b
		}
	}

	if err := s.media.UpsertPhotoMetadata(ctx, media.PhotoMetadataParams{
		ItemID:        item.ID,
		TakenAt:       ex.TakenAt,
		CameraMake:    ex.CameraMake,
		CameraModel:   ex.CameraModel,
		LensModel:     ex.LensModel,
		FocalLengthMM: ex.FocalLengthMM,
		Aperture:      ex.Aperture,
		ShutterSpeed:  ex.ShutterSpeed,
		ISO:           ex.ISO,
		Flash:         ex.Flash,
		Orientation:   ex.Orientation,
		Width:         ex.Width,
		Height:        ex.Height,
		GPSLat:        ex.GPSLat,
		GPSLon:        ex.GPSLon,
		GPSAlt:        ex.GPSAlt,
		RawEXIF:       rawJSON,
	}); err != nil {
		s.logger.WarnContext(ctx, "exif upsert failed", "path", path, "err", err)
		return
	}

	// Mirror DateTimeOriginal onto the item so the existing date-sort path
	// works for photos.
	if ex.TakenAt != nil && (item.OriginallyAvailableAt == nil || !item.OriginallyAvailableAt.Equal(*ex.TakenAt)) {
		taken := *ex.TakenAt
		s.media.UpdateItemMetadata(ctx, media.UpdateItemMetadataParams{
			ID:                    item.ID,
			Title:                 item.Title,
			SortTitle:             item.SortTitle,
			OriginallyAvailableAt: &taken,
		})
	}
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

	// 1. Find or create the "show" item (parent_id=null). When the
	//    show's root folder carries a TRaSH/Sonarr-style id marker
	//    (`{tmdb-NNN}`, `{tvdb-NNN}`, `[tvdbid-NNN]`, `{imdb-tt...}`),
	//    pass those IDs into FindOrCreateHierarchyItem so:
	//      - the find side can match an existing row by tmdb_id even
	//        if the parsed title differs (the same show ingested
	//        twice as `[ToonsHub] My Hero Academia` and then later
	//        renamed to `My Hero Academia {tmdb-65930}` collapses
	//        onto a single row);
	//      - a fresh insert starts with the IDs already set, so the
	//        enricher can `RefreshTV` directly instead of falling
	//        back to a fuzzy title search that may miss.
	folderIDs := ParseFolderIDs(filepath.Base(showDirFromFile(path)))
	createParams := media.CreateItemParams{
		LibraryID: libraryID,
		Type:      "show",
		Title:     showTitle,
		SortTitle: showTitle,
	}
	if folderIDs.TMDBID > 0 {
		t := folderIDs.TMDBID
		createParams.TMDBID = &t
	}
	if folderIDs.TVDBID > 0 {
		t := folderIDs.TVDBID
		createParams.TVDBID = &t
	}
	if folderIDs.IMDBID != "" {
		i := folderIDs.IMDBID
		createParams.IMDBID = &i
	}
	show, err := s.media.FindOrCreateHierarchyItem(ctx, createParams)
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
	// Normalize backslashes so Windows-style paths parse correctly on any host.
	path = strings.ReplaceAll(filepath.ToSlash(path), `\`, `/`)
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]

	// If the filename is purely numeric (Blu-ray stream index), use the parent dir.
	if numericNameRE.MatchString(stem) {
		dir := filepath.Base(filepath.Dir(path))
		if dir != "" && dir != "." && dir != string(os.PathSeparator) {
			return cleanTitle(StripFolderIDMarkers(dir))
		}
	}

	// Strip TRaSH/Sonarr/Radarr id markers (`{tmdb-NNN}`, `{tvdb-NNN}`,
	// `[tvdbid-NNN]`, `{imdb-tt...}`) before title parsing — the
	// markers are extracted separately by ParseFolderIDs and would
	// otherwise leak into the human-readable title.
	return cleanTitle(StripFolderIDMarkers(stem))
}

// cleanTitle normalises a raw media name (filename stem or stored title) into
// a human-readable title and optional year. It handles dot-separated,
// underscore-separated, and mixed naming, and strips everything after the year
// (resolution tags, source tags, group names, etc.).
func cleanTitle(name string) (title string, year *int) {
	// Strip a leading [release-group] prefix before any other normalization.
	// "[ToonsHub] My Hero Academia" → "My Hero Academia". Done first so the
	// bracket contents don't leak into the year regex or the search query
	// passed to TMDB. Same regex shape the SQL dedup queries use, kept in
	// sync so library scan-time matching and post-hoc dedup agree.
	name = bracketPrefixRE.ReplaceAllString(name, "")

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
	// Some release tools HTML-escape the title in the filename (e.g. "Mike.&amp;.Nick…"),
	// which breaks TMDB matching. Unescape once so the search term looks natural.
	title = html.UnescapeString(title)
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
	case "audiobook":
		return "audiobook"
	case "podcast":
		return "podcast_episode"
	default:
		return "movie"
	}
}

// musicExtensions is the set of file extensions that are audio-only. Kept in
// sync with the audio subset of validExtensions. Lossless-vs-lossy is encoded
// separately in losslessExtensions so scanner/probe can flag the media_files
// row without re-inferring from the codec string.
var musicExtensions = map[string]bool{
	".mp3": true, ".m4a": true, ".aac": true, ".ogg": true, ".opus": true,
	".flac": true, ".wav": true, ".aif": true, ".aiff": true, ".alac": true,
	".wv": true, ".ape": true, ".tak": true,
	".dsf": true, ".dff": true,
}

// audiobookExtensions are audio containers that typically hold a whole
// book (m4b chaptered MP4) or episodic audio. The scanner routes
// these through processAudiobook when the library type is
// "audiobook" — outside that library they're just regular music
// files.
var audiobookExtensions = map[string]bool{
	".m4b": true, ".m4a": true, ".mp3": true, ".aac": true,
	".ogg": true, ".opus": true, ".flac": true,
}

func isAudiobookFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return audiobookExtensions[ext]
}

// losslessExtensions flags audio containers that are bit-perfect end-to-end.
// ALAC is usually inside .m4a, so the extension alone can't distinguish it —
// the probe step has to look at the codec field for .m4a files. Everything
// here is unambiguous by extension.
var losslessExtensions = map[string]bool{
	".flac": true, ".wav": true, ".aif": true, ".aiff": true, ".alac": true,
	".wv": true, ".ape": true, ".tak": true,
	".dsf": true, ".dff": true,
}

func isMediaFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return validExtensions[ext] || imageExtensions[ext] || bookExtensions[ext]
}

// isBookFile flags container formats handled by the book scanner. v2.1
// Stage 1 is CBZ-only; CBR + EPUB land once their parsers are picked.
func isBookFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return bookExtensions[ext]
}

func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext]
}

func isMusicFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return musicExtensions[ext]
}

// videoExtensions are the container formats routed through the video
// transcode pipeline — a proper subset of validExtensions. Kept
// separate from isMediaFile so the music-library scanner can ask
// "is this file a video?" without rejecting the audio branch.
var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true,
	".mov": true, ".wmv": true, ".ts": true, ".m2ts": true,
	".webm": true,
}

func isVideoFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return videoExtensions[ext]
}

// isLosslessAudio classifies a music file as lossless or lossy. The extension
// alone is authoritative for most formats; .m4a is the one container that can
// hold either ALAC (lossless) or AAC (lossy), so the codec from ffprobe is the
// tiebreaker there. Codec passed in may be nil when ffprobe failed — in that
// case we fall back to the extension and conservatively call .m4a lossy.
func isLosslessAudio(path string, codec *string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if losslessExtensions[ext] {
		return true
	}
	if ext == ".m4a" && codec != nil {
		c := strings.ToLower(*codec)
		return c == "alac"
	}
	return false
}

// badPosterPath reports whether a stored poster_path is stale and must be
// re-fetched. A path is stale if it is absolute, contains ".." traversal,
// or uses the legacy .artwork/ directory prefix.
func badPosterPath(p string) bool {
	slashed := filepath.ToSlash(p)
	// Windows drive-letter absolute (e.g. C:\...) — caught explicitly because
	// filepath.IsAbs on Linux won't flag it.
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return true
	}
	if filepath.IsAbs(p) || strings.HasPrefix(slashed, "/") || strings.HasPrefix(slashed, "../") {
		return true
	}
	// Legacy paths that used the .artwork/ directory need re-download.
	if strings.HasPrefix(slashed, ".artwork/") {
		return true
	}
	return false
}

// enrichCooldown gates TMDB retries for items whose previous lookup came up
// empty. Items with a recent attempt (less than this ago) are skipped so a
// junk release title or a truly obscure item can't burn API quota on every
// scan. 7 days is long enough to meaningfully reduce traffic and short enough
// that a title fix or TMDB data addition is picked up within a week.
const enrichCooldown = 7 * 24 * time.Hour

// shouldEnrich decides whether to queue an item for metadata enrichment.
// New items always go through. Existing items are only re-queued when a field
// the enricher should fill is still missing (itemNeedsEnrich) AND the last
// attempt was either never made or happened more than enrichCooldown ago.
func (s *Scanner) shouldEnrich(ctx context.Context, item *media.Item, isNew bool) bool {
	if isNew {
		return true
	}
	if !itemNeedsEnrich(item) && !s.parentNeedsEnrich(ctx, item) {
		return false
	}
	attempted, err := s.media.GetEnrichAttemptedAt(ctx, item.ID)
	if err != nil || attempted == nil {
		return true
	}
	return time.Since(*attempted) >= enrichCooldown
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
		// Once the item has a poster, consider it enriched. Content rating is
		// nice-to-have for parental filters, but TMDB legitimately has no rating
		// for some titles; retriggering enrichment every scan just to hunt for
		// a rating burns quota and can trip rate limits. A separate backfill
		// pass can be added later if content ratings become mandatory.
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
