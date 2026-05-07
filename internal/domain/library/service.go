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
	ID    uuid.UUID
	Name  string
	Type  string // "movie" | "show" | "music" | "photo" | ...
	Paths []string
	Agent string
	Lang  string

	// IsPrivate gates library visibility. false (the default) means
	// every authenticated user can see it; true requires an explicit
	// row in library_access for each user. v2.1 addition — public-by-
	// default preserves v2.0 behaviour where no libraries were
	// effectively private.
	IsPrivate bool

	// AutoGrantNewUsers controls whether new accounts get this library
	// in their grants on creation. Only meaningful for IsPrivate=true
	// libraries — public libraries are visible without grants. The UI
	// gates the toggle on IsPrivate=true; setting it on a public
	// library is a no-op functionally.
	AutoGrantNewUsers bool

	ScanInterval            *time.Duration
	ScanLastCompletedAt     *time.Time
	MetadataRefreshInterval *time.Duration
	MetadataLastRefreshedAt *time.Time

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
	IsPrivate               bool
	// AutoGrantNewUsers can only be set on IsPrivate=true libraries via
	// the API path's validation; storing it on public libraries is
	// harmless but the UI hides the toggle, so this field is typically
	// only meaningful when IsPrivate is also true.
	AutoGrantNewUsers bool
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
	// Pointer fields = PATCH semantics: nil preserves the current value,
	// non-nil flips it.
	IsPrivate         *bool
	AutoGrantNewUsers *bool
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
	SoftDeleteMediaFilesByLibrary(ctx context.Context, libraryID uuid.UUID) error
	// PurgeDeletedLibraryRows hard-removes every media_items row for
	// the library; FK cascades clean up media_files, watch_state,
	// favorites, etc. Returns the number of items removed. Caller
	// must ensure the library is already soft-deleted.
	PurgeDeletedLibraryRows(ctx context.Context, libraryID uuid.UUID) (int64, error)
	RefreshHubRecentlyAdded(ctx context.Context) error
	MarkLibraryScanCompleted(ctx context.Context, id uuid.UUID) error
	MarkLibraryMetadataRefreshed(ctx context.Context, id uuid.UUID) error
	ListLibrariesDueForScan(ctx context.Context) ([]Library, error)
	ListLibrariesDueForMetadataRefresh(ctx context.Context) ([]Library, error)
	CountLibraries(ctx context.Context) (int64, error)

	// Per-user library access (ACL). Default-deny: absence of a row means the
	// user has no access. Admins bypass this table at the service layer.
	ListLibraryAccessByUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	ListAllowedLibraryIDsForUser(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
	HasLibraryAccess(ctx context.Context, userID, libraryID uuid.UUID) (bool, error)
	GrantLibraryAccess(ctx context.Context, userID, libraryID uuid.UUID) error
	GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error
	RevokeAllLibraryAccessForUser(ctx context.Context, userID uuid.UUID) error
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

// ListForUser returns libraries the user has access to. Admins get the full
// list unchanged; non-admins are filtered against the library_access table.
// Default-deny: a user with no rows in library_access sees nothing.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]Library, error) {
	if isAdmin {
		return s.List(ctx)
	}
	allowed, err := s.ro.ListAllowedLibraryIDsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list allowed library ids: %w", err)
	}
	if len(allowed) == 0 {
		return []Library{}, nil
	}
	set := make(map[uuid.UUID]struct{}, len(allowed))
	for _, id := range allowed {
		set[id] = struct{}{}
	}
	libs, err := s.ro.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	filtered := libs[:0]
	for _, lib := range libs {
		if _, ok := set[lib.ID]; ok {
			filtered = append(filtered, lib)
		}
	}
	return filtered, nil
}

// GrantAutoLibrariesToUser inserts library_access rows for every library
// flagged auto_grant_new_users. Called from every user-creation path
// (admin Create, invite accept, OIDC/SAML/LDAP auto-create) so a fresh
// account on an all-private install doesn't land on a barren home page.
//
// Errors here are logged but should not fail the user-creation
// transaction at the call site — a missing grant is a UX inconvenience,
// not a security regression (default is no access, which is safe).
// Callers decide their own error policy.
func (s *Service) GrantAutoLibrariesToUser(ctx context.Context, userID uuid.UUID) error {
	return s.rw.GrantAutoLibrariesToUser(ctx, userID)
}

// AllowedLibraryIDs returns a set of library IDs the user can access, or nil
// for admins (meaning "all libraries, no filtering needed"). Callers can use
// the nil return as a fast-path to skip filtering entirely.
func (s *Service) AllowedLibraryIDs(ctx context.Context, userID uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	ids, err := s.ro.ListAllowedLibraryIDsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list allowed library ids: %w", err)
	}
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// CanAccessLibrary returns true if the user is allowed to see the given
// library. Admins always return true. Non-admins are checked against the
// library_access table.
func (s *Service) CanAccessLibrary(ctx context.Context, userID, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	if isAdmin {
		return true, nil
	}
	ok, err := s.ro.HasLibraryAccess(ctx, userID, libraryID)
	if err != nil {
		return false, fmt.Errorf("check library access: %w", err)
	}
	return ok, nil
}

// ListAccessForUser returns every library paired with whether the user has
// been granted access — the shape the Users-tab toggle UI needs. Admins are
// reported as having access to everything.
type LibraryAccess struct {
	Library Library
	Enabled bool
}

func (s *Service) ListAccessForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]LibraryAccess, error) {
	libs, err := s.ro.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	if isAdmin {
		out := make([]LibraryAccess, len(libs))
		for i := range libs {
			out[i] = LibraryAccess{Library: libs[i], Enabled: true}
		}
		return out, nil
	}
	granted, err := s.ro.ListLibraryAccessByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list library access: %w", err)
	}
	set := make(map[uuid.UUID]struct{}, len(granted))
	for _, id := range granted {
		set[id] = struct{}{}
	}
	out := make([]LibraryAccess, len(libs))
	for i := range libs {
		_, ok := set[libs[i].ID]
		out[i] = LibraryAccess{Library: libs[i], Enabled: ok}
	}
	return out, nil
}

// ReplaceAccessForUser sets the user's library grants to exactly the given
// list. No-op for admins (they bypass the table; saving grants would be
// misleading). Library IDs that don't exist are silently skipped by the DB's
// FK constraint — callers should validate upstream if strict semantics are
// required.
func (s *Service) ReplaceAccessForUser(ctx context.Context, userID uuid.UUID, libraryIDs []uuid.UUID) error {
	if err := s.rw.RevokeAllLibraryAccessForUser(ctx, userID); err != nil {
		return fmt.Errorf("revoke all access for user %s: %w", userID, err)
	}
	for _, libID := range libraryIDs {
		if err := s.rw.GrantLibraryAccess(ctx, userID, libID); err != nil {
			return fmt.Errorf("grant library %s to user %s: %w", libID, userID, err)
		}
	}
	return nil
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

// Delete soft-deletes the libraries row (preserved for audit), then
// kicks off a detached background goroutine that hard-deletes every
// child media_items row — FK CASCADE handles media_files, watch_state,
// favorites, collection_items, intro_markers, trickplay rows,
// external_subtitles, etc. The two synchronous UPDATEs that come
// first (soft-delete on items + status='deleted' on files) close the
// "new library at same path before async finishes" window: the
// partial UNIQUE on media_files(file_path) WHERE status!='deleted'
// (00080) immediately stops recognising those rows so a fresh scan
// can claim the paths without colliding with the in-flight cleanup.
//
// The cascade DELETE itself runs detached because for a library
// with thousands of items it reliably exceeds Cloudflare's 100s
// edge timeout — synchronous would surface ERR 524 and roll back
// mid-transaction (this exact bug took down QA before this fix).
// The goroutine uses context.WithoutCancel so the HTTP-request
// cancellation doesn't propagate.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.rw.SoftDeleteLibrary(ctx, id); err != nil {
		return fmt.Errorf("delete library %s: %w", id, mapNotFound(err))
	}
	if err := s.rw.SoftDeleteMediaItemsByLibrary(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "failed to soft-delete media items for library",
			"library_id", id, "err", err)
	}
	if err := s.rw.SoftDeleteMediaFilesByLibrary(ctx, id); err != nil {
		s.logger.ErrorContext(ctx, "failed to soft-delete media files for library",
			"library_id", id, "err", err)
	}
	if err := s.rw.RefreshHubRecentlyAdded(ctx); err != nil {
		s.logger.WarnContext(ctx, "failed to refresh hub after library delete",
			"library_id", id, "err", err)
	}
	s.logger.InfoContext(ctx, "library deleted (cascade purge running async)",
		"library_id", id)

	// Hard-delete cascade — detached so a request cancellation
	// (Cloudflare 524) can't roll the transaction back partway.
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		n, err := s.rw.PurgeDeletedLibraryRows(bgCtx, id)
		if err != nil {
			s.logger.ErrorContext(bgCtx, "cascade purge after delete failed",
				"library_id", id, "err", err)
			return
		}
		s.logger.InfoContext(bgCtx, "cascade purge after delete complete",
			"library_id", id, "items_deleted", n)
	}()
	return nil
}

// PurgeDeleted hard-removes the rows for an already-soft-deleted
// library. Returns the number of media_items rows hard-deleted —
// FK cascades take care of media_files, watch_state, favorites,
// collection memberships, intro_markers, trickplay rows, etc.
//
// The "must be soft-deleted first" gate is enforced inside the SQL
// (see PurgeDeletedLibraryRows in queries/media.sql) — calling this
// on a live library returns 0 rows affected without touching
// anything, so a typo can't nuke production data.
func (s *Service) PurgeDeleted(ctx context.Context, id uuid.UUID) (int64, error) {
	n, err := s.rw.PurgeDeletedLibraryRows(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("purge library %s: %w", id, err)
	}
	s.logger.InfoContext(ctx, "library rows purged",
		"library_id", id, "items_deleted", n)
	return n, nil
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
	// `manga` is parked: the schema CHECK constraint still allows it
	// (migration 00077) and the AniList manga agent + reader code stay
	// in tree for future revival, but new libraries can't be created
	// with type=manga because the volume/chapter hierarchy and the
	// production reader UX both need more work than v2.2 had budget
	// for. Re-enabling is a one-line addition here once that work
	// lands.
	validTypes := map[string]bool{
		"movie": true, "show": true, "music": true, "anime": true,
		"photo": true, "dvr": true, "audiobook": true, "podcast": true,
		"home_video": true, "book": true,
	}
	if !validTypes[p.Type] {
		return &ValidationError{Field: "type", Message: "must be movie, show, anime, music, audiobook, podcast, photo, home_video, book, or dvr"}
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
