// Package media contains pure business logic for media item and file management.
package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

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
	ID          uuid.UUID
	MediaItemID uuid.UUID
	FilePath    string
	FileSize    int64
	Container   *string
	VideoCodec  *string
	AudioCodec  *string
	ResolutionW *int
	ResolutionH *int
	Bitrate     *int64
	HDRType     *string
	FrameRate   *float64
	AudioStreams    []byte // JSONB
	SubtitleStreams []byte // JSONB
	Chapters       []byte // JSONB
	FileHash    *string
	DurationMS  *int64
	Status      string // "active" | "missing" | "deleted"
	MissingSince *time.Time
	ScannedAt   time.Time
	CreatedAt   time.Time
}

// FilterParams holds optional filter/sort parameters for listing items.
type FilterParams struct {
	Genre     *string
	YearMin   *int
	YearMax   *int
	RatingMin *float64
	Sort      string // title, year, rating, created_at
	SortAsc   bool
}

// Querier defines the DB operations this service needs.
type Querier interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (Item, error)
	GetMediaItemByTMDBID(ctx context.Context, libraryID uuid.UUID, tmdbID int) (Item, error)
	ListMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]Item, error)
	ListMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f FilterParams) ([]Item, error)
	ListMediaItemChildren(ctx context.Context, parentID uuid.UUID) ([]Item, error)
	CreateMediaItem(ctx context.Context, p CreateItemParams) (Item, error)
	UpdateMediaItemMetadata(ctx context.Context, p UpdateItemMetadataParams) (Item, error)
	SoftDeleteMediaItem(ctx context.Context, id uuid.UUID) error
	SoftDeleteMediaItemIfAllFilesDeleted(ctx context.Context, id uuid.UUID) error
	CountMediaItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error)
	CountMediaItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f FilterParams) (int64, error)
	ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error)
	SearchMediaItems(ctx context.Context, libraryID uuid.UUID, query string, limit int32) ([]Item, error)

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
}

// CreateItemParams holds the input for creating a media item.
type CreateItemParams struct {
	LibraryID     uuid.UUID
	Type          string
	Title         string
	SortTitle     string
	OriginalTitle *string
	Year          *int
	Summary       *string
	Tagline       *string
	Rating        *float64
	AudienceRating *float64
	ContentRating *string
	DurationMS    *int64
	Genres        []string
	Tags          []string
	TMDBID        *int
	TVDBID        *int
	IMDBID        *string
	ParentID      *uuid.UUID
	Index         *int
	PosterPath    *string
	FanartPath    *string
	ThumbPath     *string
	OriginallyAvailableAt *time.Time
}

// UpdateItemMetadataParams holds the fields updated by the metadata agent.
type UpdateItemMetadataParams struct {
	ID             uuid.UUID
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
	PosterPath     *string
	FanartPath     *string
	ThumbPath      *string
	OriginallyAvailableAt *time.Time
	TMDBID         *int // optional; when non-nil, updates tmdb_id on the item
	TVDBID         *int // optional; when non-nil, updates tvdb_id on the item
}

// CreateFileParams holds the input for creating a media file record.
type CreateFileParams struct {
	MediaItemID    uuid.UUID
	FilePath       string
	FileSize       int64
	Container      *string
	VideoCodec     *string
	AudioCodec     *string
	ResolutionW    *int
	ResolutionH    *int
	Bitrate        *int64
	HDRType        *string
	FrameRate      *float64
	AudioStreams    []byte
	SubtitleStreams []byte
	Chapters       []byte
	FileHash       *string
	DurationMS     *int64
}

// Service implements media business logic with rw/ro querier split (ADR-021).
type Service struct {
	rw     Querier
	ro     Querier
	logger *slog.Logger
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
	return &file, true, nil
}

// MarkMissing marks a file as missing (first step of grace period, ADR-011).
func (s *Service) MarkMissing(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.MarkMediaFileMissing(ctx, id); err != nil {
		return fmt.Errorf("mark missing %s: %w", id, err)
	}
	return nil
}

// ListActiveFilesForLibrary returns all active files whose parent item belongs
// to the given library. Used by the scanner to detect orphaned file records.
func (s *Service) ListActiveFilesForLibrary(ctx context.Context, libraryID uuid.UUID) ([]File, error) {
	return s.rw.ListActiveFilesForLibrary(ctx, libraryID)
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

// normalizeTitle folds a title to a canonical form for deduplication: lowercase,
// non-alphanumeric characters replaced by spaces, repeated spaces collapsed.
// "Battle: Los Angeles" and "battle los angeles" both become "battle los angeles".
func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// FindOrCreateItem looks up a media item by title (and year if provided) within
// a library. If none is found it creates one with the supplied params.
// This is used by the local scanner to ensure every file has an owning item.
// Searches on rw (not ro) to avoid creating duplicates due to replication lag.
// On create failure (concurrent insert race), retries the search once.
func (s *Service) FindOrCreateItem(ctx context.Context, p CreateItemParams) (*Item, error) {
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
	if err != nil && strings.Contains(err.Error(), "no rows") {
		return ErrNotFound
	}
	return err
}
