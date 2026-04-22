// Package media contains pure business logic for media item and file management.
package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// newDiacriticStripper returns a transformer that folds characters to their
// NFD form and removes the combining marks, so "Beyoncé" and "Beyonce"
// normalize to the same key. A new transformer is returned per call because
// transform.Chain keeps internal buffers that aren't safe for concurrent
// use. Kept in sync with the SQL unaccent() extension used by dedupe queries.
func newDiacriticStripper() transform.Transformer {
	return transform.Chain(
		norm.NFD,
		runes.Remove(runes.In(unicode.Mn)),
		norm.NFC,
	)
}

var (
	ErrNotFound = errors.New("media not found")
	ErrConflict = errors.New("media already exists")
)

// Item is the domain model for a media item (movie, show, season, episode, etc.)
type Item struct {
	ID             uuid.UUID
	LibraryID      uuid.UUID
	Type           string
	Title          string
	SortTitle      string
	OriginalTitle  *string
	Year           *int
	Summary        *string
	Tagline        *string
	Rating         *float64
	AudienceRating *float64
	ContentRating  *string
	DurationMS     *int64
	Genres         []string
	Tags           []string

	// External IDs
	TMDBID *int
	TVDBID *int
	IMDBID *string

	// Hierarchy
	ParentID *uuid.UUID
	Index    *int

	// Artwork (relative paths from MEDIA_PATH)
	PosterPath *string
	FanartPath *string
	ThumbPath  *string

	OriginallyAvailableAt *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
	DeletedAt             *time.Time
}

// File represents one physical media file attached to an Item.
// An Item may have multiple Files (multi-version, ADR-031).
type File struct {
	ID              uuid.UUID
	MediaItemID     uuid.UUID
	FilePath        string
	FileSize        int64
	Container       *string
	VideoCodec      *string
	AudioCodec      *string
	ResolutionW     *int
	ResolutionH     *int
	Bitrate         *int64
	HDRType         *string
	FrameRate       *float64
	AudioStreams    []byte // JSONB
	SubtitleStreams []byte // JSONB
	Chapters        []byte // JSONB
	FileHash        *string
	DurationMS      *int64
	Status          string // "active" | "missing" | "deleted"
	MissingSince    *time.Time
	ScannedAt       time.Time
	CreatedAt       time.Time
}

// FilterParams holds optional filter/sort parameters for listing items.
type FilterParams struct {
	Genre         *string
	YearMin       *int
	YearMax       *int
	RatingMin     *float64
	MaxRatingRank *int   // content_rating_rank() ceiling for parental filtering
	Sort          string // title, year, rating, created_at
	SortAsc       bool
}

// DuplicatePair identifies a duplicate top-level item that should be merged
// into a survivor. Both IDs are media_item ids.
type DuplicatePair struct {
	LoserID    uuid.UUID
	SurvivorID uuid.UUID
}

// GenreCount is one row of a genre-browse aggregation: a genre name plus the
// number of root-type items (movies, shows, artists) that carry it.
type GenreCount struct {
	Genre string
	Count int64
}

// YearCount is one row of a year-browse aggregation.
type YearCount struct {
	Year  int32
	Count int64
}

// Querier defines the DB operations this service needs.
type Querier interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (Item, error)
	GetMediaItemByTMDBID(ctx context.Context, libraryID uuid.UUID, tmdbID int) (Item, error)
	ListMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]Item, error)
	ListMediaItemsMissingArt(ctx context.Context, limit int32) ([]Item, error)
	ListMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f FilterParams) ([]Item, error)
	ListMediaItemChildren(ctx context.Context, parentID uuid.UUID) ([]Item, error)
	CreateMediaItem(ctx context.Context, p CreateItemParams) (Item, error)
	UpdateMediaItemMetadata(ctx context.Context, p UpdateItemMetadataParams) (Item, error)
	SoftDeleteMediaItem(ctx context.Context, id uuid.UUID) error
	SoftDeleteMediaItemIfAllFilesDeleted(ctx context.Context, id uuid.UUID) error
	RestoreMediaItemAncestry(ctx context.Context, id uuid.UUID) error
	CountMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error)
	CountMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f FilterParams) (int64, error)
	ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error)
	ListGenresWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]GenreCount, error)
	ListYearsWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]YearCount, error)
	SearchMediaItems(ctx context.Context, libraryID uuid.UUID, query string, limit int32) ([]Item, error)
	FindTopLevelItemByTitleYear(ctx context.Context, libraryID uuid.UUID, itemType, title string, year *int) (*Item, error)
	FindTopLevelItemsByTitleFlexible(ctx context.Context, libraryID uuid.UUID, itemType, title string) ([]Item, error)
	ListDuplicateTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) ([]DuplicatePair, error)
	ListPrefixDuplicateTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) ([]DuplicatePair, error)
	ListDuplicateChildItems(ctx context.Context, itemType string, parentID *uuid.UUID) ([]DuplicatePair, error)
	ListCollabArtistMerges(ctx context.Context, libraryID *uuid.UUID) ([]DuplicatePair, error)
	ReparentMediaItem(ctx context.Context, id uuid.UUID, newParent *uuid.UUID) error
	ReparentMediaFilesByItem(ctx context.Context, fromItemID, toItemID uuid.UUID) error

	GetMediaFile(ctx context.Context, id uuid.UUID) (File, error)
	GetMediaFileByPath(ctx context.Context, path string) (File, error)
	GetMediaFileByHash(ctx context.Context, hash string) (File, error)
	ListMediaFilesForItem(ctx context.Context, itemID uuid.UUID) ([]File, error)
	CreateMediaFile(ctx context.Context, p CreateFileParams) (File, error)
	UpdateMediaFilePath(ctx context.Context, id uuid.UUID, newPath string) error
	MarkMediaFileMissing(ctx context.Context, id uuid.UUID) error
	MarkMediaFileActive(ctx context.Context, id uuid.UUID) error
	MarkMediaFileDeleted(ctx context.Context, id uuid.UUID) error
	UpdateMediaFileHash(ctx context.Context, id uuid.UUID, hash string) error
	UpdateMediaFileItemID(ctx context.Context, id uuid.UUID, itemID uuid.UUID) error
	UpdateMediaFileTechnicalMetadata(ctx context.Context, id uuid.UUID, p CreateFileParams) error
	ListMissingFilesOlderThan(ctx context.Context, before time.Time) ([]File, error)
	ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]File, error)
	DeleteMissingFilesByLibrary(ctx context.Context, libraryID uuid.UUID) error
	HardDeleteSoftDeletedFilesByLibrary(ctx context.Context, libraryID uuid.UUID) (int64, error)
	GetMediaItemEnrichAttemptedAt(ctx context.Context, id uuid.UUID) (*time.Time, error)
	TouchMediaItemEnrichAttempt(ctx context.Context, id uuid.UUID) error
	SoftDeleteItemsWithNoActiveFiles(ctx context.Context, libraryID uuid.UUID) error
	SoftDeleteEmptyContainerItems(ctx context.Context, libraryID uuid.UUID) error

	UpsertPhotoMetadata(ctx context.Context, p PhotoMetadataParams) error
	GetPhotoMetadata(ctx context.Context, itemID uuid.UUID) (*PhotoMetadata, error)
}

// PhotoMetadataParams carries the parsed EXIF data for one photo. All fields
// other than ItemID are optional — absent EXIF tags simply remain nil.
type PhotoMetadataParams struct {
	ItemID        uuid.UUID
	TakenAt       *time.Time
	CameraMake    *string
	CameraModel   *string
	LensModel     *string
	FocalLengthMM *float64
	Aperture      *float64
	ShutterSpeed  *string
	ISO           *int32
	Flash         *bool
	Orientation   *int32
	Width         *int32
	Height        *int32
	GPSLat        *float64
	GPSLon        *float64
	GPSAlt        *float64
	RawEXIF       []byte
}

// CreateItemParams holds the input for creating a media item.
type CreateItemParams struct {
	LibraryID             uuid.UUID
	Type                  string
	Title                 string
	SortTitle             string
	OriginalTitle         *string
	Year                  *int
	Summary               *string
	Tagline               *string
	Rating                *float64
	AudienceRating        *float64
	ContentRating         *string
	DurationMS            *int64
	Genres                []string
	Tags                  []string
	TMDBID                *int
	TVDBID                *int
	IMDBID                *string
	ParentID              *uuid.UUID
	Index                 *int
	PosterPath            *string
	FanartPath            *string
	ThumbPath             *string
	OriginallyAvailableAt *time.Time
}

// UpdateItemMetadataParams holds the fields updated by the metadata agent.
type UpdateItemMetadataParams struct {
	ID                    uuid.UUID
	Title                 string
	SortTitle             string
	OriginalTitle         *string
	Year                  *int
	Summary               *string
	Tagline               *string
	Rating                *float64
	AudienceRating        *float64
	ContentRating         *string
	DurationMS            *int64
	Genres                []string
	Tags                  []string
	PosterPath            *string
	FanartPath            *string
	ThumbPath             *string
	OriginallyAvailableAt *time.Time
	TMDBID                *int // optional; when non-nil, updates tmdb_id on the item
	TVDBID                *int // optional; when non-nil, updates tvdb_id on the item
}

// CreateFileParams holds the input for creating a media file record.
type CreateFileParams struct {
	MediaItemID     uuid.UUID
	FilePath        string
	FileSize        int64
	Container       *string
	VideoCodec      *string
	AudioCodec      *string
	ResolutionW     *int
	ResolutionH     *int
	Bitrate         *int64
	HDRType         *string
	FrameRate       *float64
	AudioStreams    []byte
	SubtitleStreams []byte
	Chapters        []byte
	FileHash        *string
	DurationMS      *int64
}

// Service implements media business logic with rw/ro querier split (ADR-021).
type Service struct {
	rw     Querier
	ro     Querier
	logger *slog.Logger

	// createMu serializes FindOrCreate* calls for the same (library, title, year)
	// to prevent duplicate items from concurrent scan goroutines.
	createMu sync.Map // key string -> *sync.Mutex
}

// NewService constructs a MediaService.
// Pass the same querier for rw and ro on single-node deployments.
func NewService(rw, ro Querier, logger *slog.Logger) *Service {
	return &Service{rw: rw, ro: ro, logger: logger}
}

// GetItem returns a media item by ID.
func (s *Service) GetItem(ctx context.Context, id uuid.UUID) (*Item, error) {
	item, err := s.ro.GetMediaItem(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get media item %s: %w", id, mapNotFound(err))
	}
	return &item, nil
}

// ListItems returns media items in a library with pagination.
func (s *Service) ListItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]Item, error) {
	items, err := s.ro.ListMediaItems(ctx, libraryID, itemType, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list media items: %w", err)
	}
	return items, nil
}

// ListItemsMissingArt returns top-level items (movies + shows) with no poster.
// Used by the maintenance backfill to re-enrich items that failed TMDB matching
// before a TVDB key was configured.
func (s *Service) ListItemsMissingArt(ctx context.Context, limit int32) ([]Item, error) {
	items, err := s.ro.ListMediaItemsMissingArt(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list media items missing art: %w", err)
	}
	return items, nil
}

// ListChildren returns children of a media item (seasons, episodes, tracks).
func (s *Service) ListChildren(ctx context.Context, parentID uuid.UUID) ([]Item, error) {
	items, err := s.ro.ListMediaItemChildren(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("list children of %s: %w", parentID, err)
	}
	return items, nil
}

// CountItems returns the total number of items of a given type in a library.
func (s *Service) CountItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error) {
	n, err := s.ro.CountMediaItems(ctx, libraryID, itemType)
	if err != nil {
		return 0, fmt.Errorf("count media items: %w", err)
	}
	return n, nil
}

// ListItemsFiltered returns media items with server-side filtering and sorting.
func (s *Service) ListItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f FilterParams) ([]Item, error) {
	items, err := s.ro.ListMediaItemsFiltered(ctx, libraryID, itemType, limit, offset, f)
	if err != nil {
		return nil, fmt.Errorf("list media items filtered: %w", err)
	}
	return items, nil
}

// CountItemsFiltered returns the count of items matching the given filters.
func (s *Service) CountItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f FilterParams) (int64, error) {
	n, err := s.ro.CountMediaItemsFiltered(ctx, libraryID, itemType, f)
	if err != nil {
		return 0, fmt.Errorf("count media items filtered: %w", err)
	}
	return n, nil
}

// ListDistinctGenres returns unique genres for a library.
func (s *Service) ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error) {
	genres, err := s.ro.ListDistinctGenres(ctx, libraryID)
	if err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}
	return genres, nil
}

// ListGenresWithCounts returns each genre and the number of root-type items
// carrying it, suitable for a browse page.
func (s *Service) ListGenresWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]GenreCount, error) {
	rows, err := s.ro.ListGenresWithCounts(ctx, libraryID, itemType)
	if err != nil {
		return nil, fmt.Errorf("list genres with counts: %w", err)
	}
	return rows, nil
}

// ListYearsWithCounts returns each release year and item count for a library.
func (s *Service) ListYearsWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]YearCount, error) {
	rows, err := s.ro.ListYearsWithCounts(ctx, libraryID, itemType)
	if err != nil {
		return nil, fmt.Errorf("list years with counts: %w", err)
	}
	return rows, nil
}

// SearchItems performs full-text search within a library.
func (s *Service) SearchItems(ctx context.Context, libraryID uuid.UUID, query string, limit int32) ([]Item, error) {
	items, err := s.ro.SearchMediaItems(ctx, libraryID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search media items: %w", err)
	}
	return items, nil
}

// GetFiles returns all files for a media item, sorted by quality descending.
func (s *Service) GetFiles(ctx context.Context, itemID uuid.UUID) ([]File, error) {
	files, err := s.ro.ListMediaFilesForItem(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("list files for item %s: %w", itemID, err)
	}
	return files, nil
}

// GetFileByPath returns the media file record for a given filesystem path.
func (s *Service) GetFileByPath(ctx context.Context, path string) (*File, error) {
	f, err := s.rw.GetMediaFileByPath(ctx, path)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return &f, nil
}

// MarkFileActive marks a media file as active (used by the scanner fast path).
func (s *Service) MarkFileActive(ctx context.Context, id uuid.UUID) error {
	return s.rw.MarkMediaFileActive(ctx, id)
}

// GetFile returns a single media file by its ID.
func (s *Service) GetFile(ctx context.Context, id uuid.UUID) (*File, error) {
	f, err := s.ro.GetMediaFile(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get file %s: %w", id, mapNotFound(err))
	}
	return &f, nil
}

// CreateOrUpdateFile upserts a media file record during a scan pass.
// If the path already exists, it updates the hash and scan time.
// If a hash match is found in missing/deleted files, it performs move detection.
// Returns the file record and whether this was a newly discovered file.
func (s *Service) CreateOrUpdateFile(ctx context.Context, p CreateFileParams) (*File, bool, error) {
	// Check if path already known.
	existing, err := s.rw.GetMediaFileByPath(ctx, p.FilePath)
	if err == nil {
		wasInactive := existing.Status != "active"
		// Path known — mark active, update hash, and refresh probe metadata.
		if err := s.rw.MarkMediaFileActive(ctx, existing.ID); err != nil {
			return nil, false, fmt.Errorf("mark file active %s: %w", existing.ID, err)
		}
		// Reassign to a different item if the scanner resolved a new owner
		// (e.g. photos that were previously collapsed by title dedup).
		if existing.MediaItemID != p.MediaItemID {
			if err := s.rw.UpdateMediaFileItemID(ctx, existing.ID, p.MediaItemID); err != nil {
				return nil, false, fmt.Errorf("reassign file item %s: %w", existing.ID, err)
			}
			existing.MediaItemID = p.MediaItemID
		}
		if p.FileHash != nil {
			if err := s.rw.UpdateMediaFileHash(ctx, existing.ID, *p.FileHash); err != nil {
				return nil, false, fmt.Errorf("update file hash %s: %w", existing.ID, err)
			}
		}
		if err := s.rw.UpdateMediaFileTechnicalMetadata(ctx, existing.ID, p); err != nil {
			return nil, false, fmt.Errorf("update file metadata %s: %w", existing.ID, err)
		}
		if wasInactive {
			if err := s.rw.RestoreMediaItemAncestry(ctx, existing.MediaItemID); err != nil {
				return nil, false, fmt.Errorf("restore ancestry for %s: %w", existing.MediaItemID, err)
			}
		}
		existing.Status = "active"
		return &existing, false, nil
	}

	// New path — check for move detection via hash.
	if p.FileHash != nil {
		if moved, err := s.rw.GetMediaFileByHash(ctx, *p.FileHash); err == nil {
			// Hash match in missing/deleted — this is a moved file (ADR-011).
			if err := s.rw.UpdateMediaFilePath(ctx, moved.ID, p.FilePath); err != nil {
				return nil, false, fmt.Errorf("update file path (move): %w", err)
			}
			if err := s.rw.RestoreMediaItemAncestry(ctx, moved.MediaItemID); err != nil {
				return nil, false, fmt.Errorf("restore ancestry for %s: %w", moved.MediaItemID, err)
			}
			s.logger.InfoContext(ctx, "file move detected",
				"old_path", moved.FilePath, "new_path", p.FilePath)
			moved.FilePath = p.FilePath
			moved.Status = "active"
			return &moved, false, nil
		}
	}

	// Genuinely new file.
	file, err := s.rw.CreateMediaFile(ctx, p)
	if err != nil {
		return nil, false, fmt.Errorf("create media file: %w", err)
	}
	if err := s.rw.RestoreMediaItemAncestry(ctx, file.MediaItemID); err != nil {
		return nil, false, fmt.Errorf("restore ancestry for %s: %w", file.MediaItemID, err)
	}
	return &file, true, nil
}

// MarkMissing marks a file as missing (first step of grace period, ADR-011).
func (s *Service) MarkMissing(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.MarkMediaFileMissing(ctx, id); err != nil {
		return fmt.Errorf("mark missing %s: %w", id, err)
	}
	return nil
}

// DeleteFile marks a file as deleted immediately (skipping the missing grace period).
func (s *Service) DeleteFile(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.MarkMediaFileDeleted(ctx, id); err != nil {
		return fmt.Errorf("delete file %s: %w", id, err)
	}
	return nil
}

// SoftDeleteItemIfEmpty soft-deletes a media item if all its files are deleted.
func (s *Service) SoftDeleteItemIfEmpty(ctx context.Context, id uuid.UUID) error {
	return s.rw.SoftDeleteMediaItemIfAllFilesDeleted(ctx, id)
}

// RestoreItemAncestry clears deleted_at on the item and all its soft-deleted
// ancestors. Called when a file re-appears so that a previously orphaned
// show/season/album is visible again instead of stuck in the soft-deleted
// graveyard.
func (s *Service) RestoreItemAncestry(ctx context.Context, id uuid.UUID) error {
	return s.rw.RestoreMediaItemAncestry(ctx, id)
}

// ListActiveFilesForLibrary returns all active files whose parent item belongs
// to the given library. Used by the scanner to detect orphaned file records.
func (s *Service) ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]File, error) {
	return s.rw.ListActiveFilesForLibrary(ctx, libraryID)
}

// CleanupMissingFiles promotes all missing files to deleted for a library.
func (s *Service) CleanupMissingFiles(ctx context.Context, libraryID uuid.UUID) error {
	return s.rw.DeleteMissingFilesByLibrary(ctx, libraryID)
}

// PurgeDeletedFiles permanently removes file rows with status='deleted' for a
// library. Called after CleanupMissingFiles so the missing-grace-period has
// already elapsed for any file that gets purged here. watch_events.file_id
// uses ON DELETE SET NULL so playback history is preserved.
func (s *Service) PurgeDeletedFiles(ctx context.Context, libraryID uuid.UUID) (int64, error) {
	return s.rw.HardDeleteSoftDeletedFilesByLibrary(ctx, libraryID)
}

// GetEnrichAttemptedAt returns the timestamp of the last enrichment attempt
// for an item (TMDB/TVDB lookup + artwork fetch), or nil if never attempted.
func (s *Service) GetEnrichAttemptedAt(ctx context.Context, id uuid.UUID) (*time.Time, error) {
	return s.ro.GetMediaItemEnrichAttemptedAt(ctx, id)
}

// TouchEnrichAttempt records that an enrichment pass ran for this item,
// whether or not any data was found. Acts as a negative cache so the scanner
// doesn't re-query TMDB for titles it can't match on every subsequent scan.
func (s *Service) TouchEnrichAttempt(ctx context.Context, id uuid.UUID) error {
	return s.rw.TouchMediaItemEnrichAttempt(ctx, id)
}

// CleanupEmptyItems soft-deletes leaf items with no active files, then
// cascades soft-delete up through container items (season → show, album →
// artist) whose children just died. Two container passes are needed because
// each pass observes the previous pass's commits via separate snapshots.
func (s *Service) CleanupEmptyItems(ctx context.Context, libraryID uuid.UUID) error {
	if err := s.rw.SoftDeleteItemsWithNoActiveFiles(ctx, libraryID); err != nil {
		return err
	}
	if err := s.rw.SoftDeleteEmptyContainerItems(ctx, libraryID); err != nil {
		return err
	}
	return s.rw.SoftDeleteEmptyContainerItems(ctx, libraryID)
}

// PromoteExpiredMissing finds files that have been missing longer than the
// grace period and marks them deleted. Then soft-deletes any media_items
// whose all files are now deleted. Returns the count of promoted files.
func (s *Service) PromoteExpiredMissing(ctx context.Context, gracePeriod time.Duration) (int, error) {
	cutoff := time.Now().Add(-gracePeriod)
	files, err := s.rw.ListMissingFilesOlderThan(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("list missing files: %w", err)
	}

	affected := map[uuid.UUID]struct{}{}
	for _, f := range files {
		if err := s.rw.MarkMediaFileDeleted(ctx, f.ID); err != nil {
			s.logger.WarnContext(ctx, "failed to mark file deleted",
				"file_id", f.ID, "err", err)
			continue
		}
		affected[f.MediaItemID] = struct{}{}
		s.logger.InfoContext(ctx, "file promoted to deleted",
			"file_id", f.ID, "path", f.FilePath)
	}

	// Cascade: soft-delete parent items with no remaining active/missing files.
	for itemID := range affected {
		if err := s.rw.SoftDeleteMediaItemIfAllFilesDeleted(ctx, itemID); err != nil {
			s.logger.WarnContext(ctx, "failed to check item for soft-delete",
				"item_id", itemID, "err", err)
		}
	}

	return len(files), nil
}

// UpdateItemMetadata updates the metadata fields of an existing media item.
// Called by the metadata enricher after a successful TMDB lookup.
func (s *Service) UpdateItemMetadata(ctx context.Context, p UpdateItemMetadataParams) (*Item, error) {
	item, err := s.rw.UpdateMediaItemMetadata(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("update item metadata %s: %w", p.ID, err)
	}
	return &item, nil
}

// UpsertPhotoMetadata writes the per-photo EXIF row, replacing any prior data
// for this item. Called by the scanner after extracting EXIF from an image.
func (s *Service) UpsertPhotoMetadata(ctx context.Context, p PhotoMetadataParams) error {
	if err := s.rw.UpsertPhotoMetadata(ctx, p); err != nil {
		return fmt.Errorf("upsert photo metadata %s: %w", p.ItemID, err)
	}
	return nil
}

// PhotoMetadata is the domain representation of a photo's EXIF data. Returned
// by GetPhotoMetadata for display in the photo viewer.
type PhotoMetadata struct {
	ItemID        uuid.UUID
	TakenAt       *time.Time
	CameraMake    *string
	CameraModel   *string
	LensModel     *string
	FocalLengthMM *float64
	Aperture      *float64
	ShutterSpeed  *string
	ISO           *int32
	Flash         *bool
	Orientation   *int32
	Width         *int32
	Height        *int32
	GPSLat        *float64
	GPSLon        *float64
	GPSAlt        *float64
}

// GetPhotoMetadata returns the EXIF row for a photo item. ErrNotFound when
// the item has no EXIF row (e.g. PNG without an EXIF block).
func (s *Service) GetPhotoMetadata(ctx context.Context, itemID uuid.UUID) (*PhotoMetadata, error) {
	pm, err := s.ro.GetPhotoMetadata(ctx, itemID)
	if err != nil {
		return nil, err
	}
	return pm, nil
}

// normalizeTitle folds a title to a canonical form for deduplication:
// lowercase, diacritics stripped, leading article stripped ("the"/"a"/"an"),
// "&" folded to "and", every non-alphanumeric character removed entirely.
// "The Beatles" and "beatles" both become "beatles"; "AC/DC" and "ACDC" both
// become "acdc"; "Rock & Roll" and "Rock and Roll" both become "rockandroll";
// "Beyoncé" and "Beyonce" both become "beyonce". Punctuation and whitespace
// differences never block a match. Year tokens survive because digits are
// preserved. Kept in sync with the SQL used by ListDuplicateTopLevelItems /
// ListDuplicateChildItems so that runtime find-or-create matches the
// post-scan dedupe pass.
func normalizeTitle(s string) string {
	if folded, _, err := transform.String(newDiacriticStripper(), s); err == nil {
		s = folded
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "'", "")
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(s, article) {
			s = s[len(article):]
			break
		}
	}
	// Fold " & " / " and " to a single "and" token so either spelling matches.
	s = andWordRE.ReplaceAllString(s, "and")
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, s)
}

var andWordRE = regexp.MustCompile(`\s+(and|&)\s+`)

// createMuFor returns a per-key mutex that serializes FindOrCreate* calls for
// the same (library, type, title, year) tuple, preventing duplicate inserts.
func (s *Service) createMuFor(p CreateItemParams) *sync.Mutex {
	year := ""
	if p.Year != nil {
		year = fmt.Sprint(*p.Year)
	}
	key := fmt.Sprintf("%s:%s:%s:%s", p.LibraryID, p.Type, normalizeTitle(p.Title), year)
	mu, _ := s.createMu.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// FindOrCreateItem looks up a media item by title (and year if provided) within
// a library. If none is found it creates one with the supplied params.
// This is used by the local scanner to ensure every file has an owning item.
// Searches on rw (not ro) to avoid creating duplicates due to replication lag.
// Serialized per (library, type, title, year) to prevent concurrent duplicate inserts.
func (s *Service) FindOrCreateItem(ctx context.Context, p CreateItemParams) (*Item, error) {
	mu := s.createMuFor(p)
	mu.Lock()
	defer mu.Unlock()

	if found := s.findItemByTitle(ctx, p); found != nil {
		return found, nil
	}
	item, err := s.rw.CreateMediaItem(ctx, p)
	if err != nil {
		// If a concurrent goroutine just created the same item, retry search.
		if found := s.findItemByTitle(ctx, p); found != nil {
			return found, nil
		}
		return nil, fmt.Errorf("create media item: %w", err)
	}
	return &item, nil
}

func (s *Service) findItemByTitle(ctx context.Context, p CreateItemParams) *Item {
	if p.Title == "" {
		return nil
	}
	// Exact-match lookup aligned with the unique partial index. This avoids
	// the LIMIT 10 full-text search missing a show whose title is also present
	// in its episodes' filenames (which would otherwise crowd out the show row).
	if found, err := s.rw.FindTopLevelItemByTitleYear(ctx, p.LibraryID, p.Type, p.Title, p.Year); err == nil && found != nil {
		return found
	}
	// Flexible lookup: matches title or original_title case-insensitively, ignores year.
	// Catches the post-enrichment duplicate case where TMDB renamed the row or
	// added a year that wasn't present in the raw filename.
	if flex, err := s.rw.FindTopLevelItemsByTitleFlexible(ctx, p.LibraryID, p.Type, p.Title); err == nil && len(flex) > 0 {
		if found := pickFlexibleMatch(flex, p.Year); found != nil {
			return found
		}
	}
	results, err := s.rw.SearchMediaItems(ctx, p.LibraryID, p.Title, 10)
	if err != nil {
		return nil
	}
	normP := normalizeTitle(p.Title)
	for i := range results {
		r := &results[i]
		if r.Type != p.Type {
			continue
		}
		if normalizeTitle(r.Title) != normP {
			continue
		}
		if p.Year != nil && r.Year != nil && *r.Year != *p.Year {
			continue
		}
		return r
	}
	return nil
}

// pickFlexibleMatch chooses the best candidate from FindTopLevelItemsByTitleFlexible
// given the scanner-side year (which may be nil). Rules:
//   - If scanner has no year, return the first candidate (already ordered by
//     enrichment richness in SQL).
//   - If scanner has a year, prefer a candidate with the same year. If none
//     match and a candidate has no year, return that one (un-enriched row
//     waiting to be enriched). Otherwise return nil — different years means
//     different shows that happen to share a title.
func pickFlexibleMatch(candidates []Item, year *int) *Item {
	if len(candidates) == 0 {
		return nil
	}
	if year == nil {
		c := candidates[0]
		return &c
	}
	var noYear *Item
	for i := range candidates {
		c := &candidates[i]
		if c.Year != nil && *c.Year == *year {
			return c
		}
		if c.Year == nil && noYear == nil {
			noYear = c
		}
	}
	return noYear
}

// FindTopLevelItem looks up a top-level item (parent_id IS NULL) by
// library+type+title without creating one. Returns (nil, nil) if not found.
// Used by the music scanner to decide whether a collab tag like
// "Elton John & Bonnie Raitt" should be folded into an existing "Elton John".
func (s *Service) FindTopLevelItem(ctx context.Context, libraryID uuid.UUID, itemType, title string) (*Item, error) {
	if title == "" {
		return nil, nil
	}
	p := CreateItemParams{LibraryID: libraryID, Type: itemType, Title: title}
	if found := s.findItemByTitle(ctx, p); found != nil {
		return found, nil
	}
	return nil, nil
}

// FindOrCreateHierarchyItem finds an existing media item matching
// (library_id, type, title, parent_id) or creates one. This supports the
// artist->album->track hierarchy used by music libraries and the
// show->season->episode hierarchy used by TV libraries.
//
// Unlike FindOrCreateItem (which uses full-text search), this method uses
// ListMediaItemChildren for parented items and SearchMediaItems for top-level
// items, then filters by normalized title. This ensures hierarchical items
// (artists, albums, seasons) are matched precisely within their parent context.
func (s *Service) FindOrCreateHierarchyItem(ctx context.Context, p CreateItemParams) (*Item, error) {
	mu := s.createMuFor(p)
	mu.Lock()
	defer mu.Unlock()

	if found := s.findHierarchyItem(ctx, p); found != nil {
		return found, nil
	}
	item, err := s.rw.CreateMediaItem(ctx, p)
	if err != nil {
		// Concurrent insert race — retry search.
		if found := s.findHierarchyItem(ctx, p); found != nil {
			return found, nil
		}
		return nil, fmt.Errorf("create hierarchy item: %w", err)
	}
	return &item, nil
}

// findHierarchyItem searches for a matching item in the hierarchy.
// For parented items it lists children of the parent; for top-level items
// it uses the full-text search index.
// When Index is set on the params, parented items are matched by type+index
// (e.g. season 1, episode 3) which is more reliable than title matching for
// items whose title may change after enrichment.
func (s *Service) findHierarchyItem(ctx context.Context, p CreateItemParams) *Item {
	if p.ParentID != nil {
		// Parented item: search among siblings.
		children, err := s.rw.ListMediaItemChildren(ctx, *p.ParentID)
		if err != nil {
			return nil
		}
		for i := range children {
			c := &children[i]
			if c.Type != p.Type {
				continue
			}
			// Prefer index-based matching (seasons and episodes).
			if p.Index != nil && c.Index != nil && *c.Index == *p.Index {
				return c
			}
			// Fall back to title matching (e.g. named items).
			if p.Title != "" && normalizeTitle(c.Title) == normalizeTitle(p.Title) {
				return c
			}
		}
		return nil
	}

	// Top-level item (e.g. show, artist): use full-text search.
	if p.Title == "" {
		return nil
	}
	return s.findItemByTitle(ctx, p)
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// DedupeResult summarizes the outcome of DedupeTopLevelItems.
type DedupeResult struct {
	MergedItems    int `json:"merged_items"`    // top-level shows/movies soft-deleted
	MergedSeasons  int `json:"merged_seasons"`  // duplicate seasons collapsed
	MergedEpisodes int `json:"merged_episodes"` // duplicate episodes collapsed
	ReparentedRows int `json:"reparented_rows"` // children moved to a survivor
}

// DedupeTopLevelItems finds duplicate top-level items (movies, shows) in the
// given library — or every library if libraryID is nil — and merges each
// duplicate into the most-enriched survivor. For shows it walks seasons and
// episodes, merging by index (collisions cause file reparenting; new
// children are simply moved). After children are moved, losers are
// soft-deleted. Safe to run repeatedly.
func (s *Service) DedupeTopLevelItems(ctx context.Context, itemType string, libraryID *uuid.UUID) (DedupeResult, error) {
	var res DedupeResult
	exactPairs, err := s.rw.ListDuplicateTopLevelItems(ctx, itemType, libraryID)
	if err != nil {
		return res, fmt.Errorf("list duplicate top-level items: %w", err)
	}
	if err := s.applyDedupePairs(ctx, exactPairs, itemType, &res); err != nil {
		return res, err
	}
	// Second pass: collapse unenriched rows whose normalized title is a
	// word-boundary prefix-extension of an enriched survivor (e.g. folder
	// "Adventure Time With Finn And Jake" → enriched "Adventure Time" 2010).
	prefixPairs, err := s.rw.ListPrefixDuplicateTopLevelItems(ctx, itemType, libraryID)
	if err != nil {
		return res, fmt.Errorf("list prefix duplicate top-level items: %w", err)
	}
	if err := s.applyDedupePairs(ctx, prefixPairs, itemType, &res); err != nil {
		return res, err
	}
	return res, nil
}

// MergeCollabArtists finds collaboration-style artist rows ("Elton John &
// Bonnie Raitt", "The Black Eyed Peas, CL") whose primary name already exists
// as a standalone artist in the same library, and merges the collab row into
// the primary. This catches collab tags scanned before the primary artist
// existed (scan-order dependent). Safe to run repeatedly.
func (s *Service) MergeCollabArtists(ctx context.Context, libraryID *uuid.UUID) (DedupeResult, error) {
	var res DedupeResult
	pairs, err := s.rw.ListCollabArtistMerges(ctx, libraryID)
	if err != nil {
		return res, fmt.Errorf("list collab artist merges: %w", err)
	}
	if err := s.applyDedupePairs(ctx, pairs, "artist", &res); err != nil {
		return res, err
	}
	return res, nil
}

// DedupeChildItems finds duplicate parented items of the given type under a
// specific parent — or every parent if parentID is nil — and merges each into
// the most-enriched survivor. Use case: collapse duplicate album rows under
// one artist caused by inconsistent tag spellings across a release's tracks.
// Safe to run repeatedly.
func (s *Service) DedupeChildItems(ctx context.Context, itemType string, parentID *uuid.UUID) (DedupeResult, error) {
	var res DedupeResult
	pairs, err := s.rw.ListDuplicateChildItems(ctx, itemType, parentID)
	if err != nil {
		return res, fmt.Errorf("list duplicate child items: %w", err)
	}
	if err := s.applyDedupePairs(ctx, pairs, itemType, &res); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Service) applyDedupePairs(ctx context.Context, pairs []DuplicatePair, itemType string, res *DedupeResult) error {
	for _, pair := range pairs {
		mergedSeasons, mergedEps, reparented, err := s.mergeChildren(ctx, pair.LoserID, pair.SurvivorID, itemType)
		if err != nil {
			return fmt.Errorf("merge %s into %s: %w", pair.LoserID, pair.SurvivorID, err)
		}
		res.MergedSeasons += mergedSeasons
		res.MergedEpisodes += mergedEps
		res.ReparentedRows += reparented
		if err := s.rw.ReparentMediaFilesByItem(ctx, pair.LoserID, pair.SurvivorID); err != nil {
			return fmt.Errorf("reparent files for %s: %w", pair.LoserID, err)
		}
		if err := s.rw.SoftDeleteMediaItem(ctx, pair.LoserID); err != nil {
			return fmt.Errorf("soft-delete loser %s: %w", pair.LoserID, err)
		}
		res.MergedItems++
	}
	return nil
}

// mergeChildren merges loser's direct children into survivor. For each loser
// child it picks a matching survivor child by index (seasons) or by index
// (episodes); on collision it recursively merges and soft-deletes the loser
// child. Otherwise it reparents the loser child to survivor. Returns counts
// of merged seasons, merged episodes, and rows reparented (not merged).
func (s *Service) mergeChildren(ctx context.Context, loserID, survivorID uuid.UUID, parentType string) (mergedSeasons, mergedEps, reparented int, err error) {
	loserKids, err := s.rw.ListMediaItemChildren(ctx, loserID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list loser children: %w", err)
	}
	if len(loserKids) == 0 {
		return 0, 0, 0, nil
	}
	survivorKids, err := s.rw.ListMediaItemChildren(ctx, survivorID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list survivor children: %w", err)
	}
	// Index survivor children by (type, index) for O(1) collision lookup.
	type key struct {
		t   string
		idx int
	}
	byKey := make(map[key]*Item, len(survivorKids))
	for i := range survivorKids {
		c := &survivorKids[i]
		if c.Index == nil {
			continue
		}
		byKey[key{c.Type, *c.Index}] = c
	}
	for i := range loserKids {
		lk := &loserKids[i]
		if lk.Index == nil {
			// No index — reparent unconditionally.
			if err := s.rw.ReparentMediaItem(ctx, lk.ID, &survivorID); err != nil {
				return 0, 0, 0, fmt.Errorf("reparent %s: %w", lk.ID, err)
			}
			reparented++
			continue
		}
		match, collide := byKey[key{lk.Type, *lk.Index}]
		if !collide {
			if err := s.rw.ReparentMediaItem(ctx, lk.ID, &survivorID); err != nil {
				return 0, 0, 0, fmt.Errorf("reparent %s: %w", lk.ID, err)
			}
			reparented++
			continue
		}
		// Collision: recurse so episodes under a duplicate season get merged
		// into the survivor season's episodes (by episode index).
		ms, me, rp, err := s.mergeChildren(ctx, lk.ID, match.ID, lk.Type)
		if err != nil {
			return 0, 0, 0, err
		}
		mergedSeasons += ms
		mergedEps += me
		reparented += rp
		if err := s.rw.ReparentMediaFilesByItem(ctx, lk.ID, match.ID); err != nil {
			return 0, 0, 0, fmt.Errorf("reparent files for %s: %w", lk.ID, err)
		}
		if err := s.rw.SoftDeleteMediaItem(ctx, lk.ID); err != nil {
			return 0, 0, 0, fmt.Errorf("soft-delete %s: %w", lk.ID, err)
		}
		switch lk.Type {
		case "season":
			mergedSeasons++
		case "episode":
			mergedEps++
		}
	}
	_ = parentType
	return mergedSeasons, mergedEps, reparented, nil
}
