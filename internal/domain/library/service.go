// Package library contains pure business logic for library management.
// This package has zero knowledge of HTTP or SQL — it operates through
// interfaces and is injected with generated DB queriers (ADR-021).
package library

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sentinel errors returned by this package.
var (
	ErrNotFound = errors.New("library not found")
	ErrConflict = errors.New("library already exists")
)

// Library is the domain model for a media library.
type Library struct {
	ID     uuid.UUID
	Name   string
	Type   string // "movie" | "show" | "music" | "photo"
	Paths  []string
	Agent  string
	Lang   string

	ScanInterval               *time.Duration
	ScanLastCompletedAt        *time.Time
	MetadataRefreshInterval    *time.Duration
	MetadataLastRefreshedAt    *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// CreateLibraryParams holds the input for creating a new library.
type CreateLibraryParams struct {
	Name                    string
	Type                    string
	Paths                   []string
	Agent                   string
	Lang                    string
	ScanInterval            time.Duration
	MetadataRefreshInterval time.Duration
}

// UpdateLibraryParams holds the fields that can be updated.
type UpdateLibraryParams struct {
	ID                      uuid.UUID
	Name                    string
	Paths                   []string
	Agent                   string
	Lang                    string
	ScanInterval            time.Duration
	MetadataRefreshInterval time.Duration
}

// Querier is the subset of gen.Querier that this service needs.
// Defined here (where used) per architecture rules.
type Querier interface {
	GetLibrary(ctx context.Context, id uuid.UUID) (Library, error)
	ListLibraries(ctx context.Context) ([]Library, error)
	CreateLibrary(ctx context.Context, p CreateLibraryParams) (Library, error)
	UpdateLibrary(ctx context.Context, p UpdateLibraryParams) (Library, error)
	SoftDeleteLibrary(ctx context.Context, id uuid.UUID) error
	SoftDeleteMediaItemsByLibrary(ctx context.Context, libraryID uuid.UUID) error
	RefreshHubRecentlyAdded(ctx context.Context) error
	MarkLibraryScanCompleted(ctx context.Context, id uuid.UUID) error
	MarkLibraryMetadataRefreshed(ctx context.Context, id uuid.UUID) error
	ListLibrariesDueForScan(ctx context.Context) ([]Library, error)
	ListLibrariesDueForMetadataRefresh(ctx context.Context) ([]Library, error)
	CountLibraries(ctx context.Context) (int64, error)
}

// ScanEnqueuer is called when a library needs a scan job dispatched.
type ScanEnqueuer interface {
	EnqueueScan(ctx context.Context, libraryID uuid.UUID) error
}

// Service implements library management business logic.
type Service struct {
	rw     Querier
	ro     Querier
	enq    ScanEnqueuer
	logger *slog.Logger
}

// NewService creates a LibraryService with read/write and read-only queriers.
// When there is no replica, pass the same querier for both rw and ro.
func NewService(rw, ro Querier, enq ScanEnqueuer, logger *slog.Logger) *Service {
	return &Service{rw: rw, ro: ro, enq: enq, logger: logger}
}

// Get returns a single library by ID.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Library, error) {
	lib, err := s.ro.GetLibrary(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get library %s: %w", id, mapNotFound(err))
	}
	return &lib, nil
}

// List returns all non-deleted libraries.
func (s *Service) List(ctx context.Context) ([]Library, error) {
	libs, err := s.ro.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	return libs, nil
}

// Create creates a new library and enqueues an initial scan.
func (s *Service) Create(ctx context.Context, p CreateLibraryParams) (*Library, error) {
	if err := validateCreateParams(p); err != nil {
		return nil, err
	}

	lib, err := s.rw.CreateLibrary(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("create library: %w", err)
	}

	// Enqueue initial scan immediately after creation.
	if err := s.enq.EnqueueScan(ctx, lib.ID); err != nil {
		s.logger.WarnContext(ctx, "failed to enqueue initial scan",
			"library_id", lib.ID, "err", err)
	}

	s.logger.InfoContext(ctx, "library created", "library_id", lib.ID, "name", lib.Name)
	return &lib, nil
}

// Update updates a library's mutable fields.
func (s *Service) Update(ctx context.Context, p UpdateLibraryParams) (*Library, error) {
	lib, err := s.rw.UpdateLibrary(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("update library %s: %w", p.ID, mapNotFound(err))
	}
	s.logger.InfoContext(ctx, "library updated", "library_id", lib.ID)
	return &lib, nil
}

// Delete soft-deletes a library and its media items, then refreshes hub views.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.SoftDeleteLibrary(ctx, id); err != nil {
		return fmt.Errorf("delete library %s: %w", id, mapNotFound(err))
	}
	if err := s.rw.SoftDeleteMediaItemsByLibrary(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "failed to soft-delete media items for library",
			"library_id", id, "err", err)
	}
	if err := s.rw.RefreshHubRecentlyAdded(ctx); err != nil {
		s.logger.WarnContext(ctx, "failed to refresh hub after library delete",
			"library_id", id, "err", err)
	}
	s.logger.InfoContext(ctx, "library deleted", "library_id", id)
	return nil
}

// EnqueueScan triggers an on-demand library scan.
func (s *Service) EnqueueScan(ctx context.Context, id uuid.UUID) error {
	// Verify the library exists before enqueuing.
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if err := s.enq.EnqueueScan(ctx, id); err != nil {
		return fmt.Errorf("enqueue scan for library %s: %w", id, err)
	}
	return nil
}

// ListDueForScan returns libraries whose scan interval has elapsed.
func (s *Service) ListDueForScan(ctx context.Context) ([]Library, error) {
	libs, err := s.ro.ListLibrariesDueForScan(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries due for scan: %w", err)
	}
	return libs, nil
}

// MarkScanCompleted records that a scan has just finished for the library,
// resetting the interval timer.
func (s *Service) MarkScanCompleted(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.MarkLibraryScanCompleted(ctx, id); err != nil {
		return fmt.Errorf("mark scan completed %s: %w", id, err)
	}
	return nil
}

// IsSetupRequired returns true when no libraries and no users exist yet.
// Used by the first-run wizard (ADR-023).
func (s *Service) IsSetupRequired(ctx context.Context) (bool, error) {
	count, err := s.ro.CountLibraries(ctx)
	if err != nil {
		return false, fmt.Errorf("count libraries: %w", err)
	}
	return count == 0, nil
}

func validateCreateParams(p CreateLibraryParams) error {
	if p.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	validTypes := map[string]bool{"movie": true, "show": true, "music": true, "photo": true}
	if !validTypes[p.Type] {
		return &ValidationError{Field: "type", Message: "must be movie, show, music, or photo"}
	}
	if len(p.Paths) == 0 {
		return &ValidationError{Field: "scan_paths", Message: "at least one path required"}
	}
	return nil
}

// ValidationError is returned when input fails domain validation.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %s: %s", e.Field, e.Message)
}

// mapNotFound translates a DB "no rows" error to ErrNotFound.
// Other errors pass through unchanged.
func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
