// Package v1 implements the OnScreen native API (/api/v1/).
// All responses use the standard envelope: { "data": ... } / { "error": ... }
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// LibraryResponse is the JSON shape for a library in the v1 API.
//
// ScanPaths leaks server filesystem layout (`/mnt/storage/movies` etc.)
// and is admin-only — non-admin members of a library see the row but
// not its on-disk roots, since absolute paths are useful only to the
// operator and double as recon for path-traversal/SSRF chaining. The
// `omitempty` here pairs with toLibraryResponse's per-call decision
// to populate the slice or leave it nil.
type LibraryResponse struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	ScanPaths           []string `json:"scan_paths,omitempty"`
	Agent               string   `json:"agent"`
	Language            string   `json:"language"`
	ScanIntervalMinutes *int     `json:"scan_interval_minutes,omitempty"`
	// IsPrivate gates visibility: false means every authenticated user
	// can see this library; true requires an explicit row in
	// library_access. v2.1 addition; default false preserves the v2.0
	// "everyone with auth sees everything" behaviour on existing rows.
	IsPrivate bool `json:"is_private"`
	// AutoGrantNewUsers: when true, every newly-created user (invite,
	// OIDC/SAML/LDAP JIT, admin Create) is automatically granted
	// access. Only meaningful when IsPrivate=true; the frontend hides
	// the toggle on public libraries since the grant is a no-op.
	AutoGrantNewUsers bool   `json:"auto_grant_new_users"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// toLibraryResponse converts a domain Library into the API response.
// includeScanPaths is true only for admin callers: regular users see
// the library row (name/type/etc.) but not the absolute on-disk paths,
// which would leak server filesystem layout to non-admins.
func toLibraryResponse(lib *library.Library, includeScanPaths bool) LibraryResponse {
	r := LibraryResponse{
		ID:                lib.ID.String(),
		Name:              lib.Name,
		Type:              lib.Type,
		Agent:             lib.Agent,
		Language:          lib.Lang,
		IsPrivate:         lib.IsPrivate,
		AutoGrantNewUsers: lib.AutoGrantNewUsers,
		CreatedAt:         lib.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         lib.UpdatedAt.Format(time.RFC3339),
	}
	if includeScanPaths {
		paths := lib.Paths
		if paths == nil {
			paths = []string{}
		}
		r.ScanPaths = paths
	}
	if lib.ScanInterval != nil {
		mins := int(lib.ScanInterval.Minutes())
		r.ScanIntervalMinutes = &mins
	}
	return r
}

// MediaItemLister is an optional service for listing items within a library.
type MediaItemLister interface {
	ListItems(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32) ([]media.Item, error)
	ListItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, limit, offset int32, f media.FilterParams) ([]media.Item, error)
	CountItems(ctx context.Context, libraryID uuid.UUID, itemType string) (int64, error)
	CountItemsFiltered(ctx context.Context, libraryID uuid.UUID, itemType string, f media.FilterParams) (int64, error)
	ListDistinctGenres(ctx context.Context, libraryID uuid.UUID) ([]string, error)
	ListGenresWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.GenreCount, error)
	ListYearsWithCounts(ctx context.Context, libraryID uuid.UUID, itemType string) ([]media.YearCount, error)
	ListEventCollectionsForLibrary(ctx context.Context, libraryID uuid.UUID) ([]media.EventCollection, error)
}

// MediaItemResponse is the JSON shape for a media item in the v1 API.
type MediaItemResponse struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Type          string    `json:"type"`
	Year          *int      `json:"year,omitempty"`
	Summary       *string   `json:"summary,omitempty"`
	Rating        *float64  `json:"rating,omitempty"`
	DurationMS    *int64    `json:"duration_ms,omitempty"`
	Genres        []string  `json:"genres,omitempty"`
	PosterPath    *string   `json:"poster_path,omitempty"`
	// OriginalTitle: foreign-language title for movies, author for
	// audiobooks (the scanner stashes the parsed author here in v2.0
	// to avoid a migration just for one column — see audiobookscan.go).
	// v2.1 surfaces it so the library grid can show "by Author" under
	// the audiobook title without a second API round-trip.
	OriginalTitle *string `json:"original_title,omitempty"`
	// TakenAt mirrors media_items.originally_available_at — for photos
	// it's EXIF DateTimeOriginal, for home videos it's file mtime, for
	// movies/episodes it's the TMDB release date. Lets the library
	// page render date-grouped headers without a second round-trip.
	TakenAt   *time.Time `json:"taken_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt int64      `json:"updated_at"`
}

// LibraryServiceIface defines the domain operations the handler needs.
type LibraryServiceIface interface {
	Get(ctx context.Context, id uuid.UUID) (*library.Library, error)
	List(ctx context.Context) ([]library.Library, error)
	ListForUser(ctx context.Context, userID uuid.UUID, isAdmin bool) ([]library.Library, error)
	CanAccessLibrary(ctx context.Context, userID, libraryID uuid.UUID, isAdmin bool) (bool, error)
	Create(ctx context.Context, p library.CreateLibraryParams) (*library.Library, error)
	Update(ctx context.Context, p library.UpdateLibraryParams) (*library.Library, error)
	Delete(ctx context.Context, id uuid.UUID) error
	EnqueueScan(ctx context.Context, id uuid.UUID) error
}

// IntroDetectorRunner runs intro/credits detection for a show library. Called
// from the admin "Detect intros now" endpoint.
type IntroDetectorRunner interface {
	DetectLibrary(ctx context.Context, libraryID uuid.UUID) error
}

// LibraryHandler handles /api/v1/libraries.
type LibraryHandler struct {
	svc      LibraryServiceIface
	media    MediaItemLister // optional; enables GET /libraries/:id/items
	detector IntroDetectorRunner
	logger   *slog.Logger
	audit    *audit.Logger // optional; nil disables admin-action audit logging
}

// NewLibraryHandler creates a LibraryHandler.
func NewLibraryHandler(svc LibraryServiceIface, logger *slog.Logger) *LibraryHandler {
	return &LibraryHandler{svc: svc, logger: logger}
}

// WithMedia wires the optional media item lister.
func (h *LibraryHandler) WithMedia(m MediaItemLister) *LibraryHandler {
	h.media = m
	return h
}

// WithDetector wires the optional intro detector for the admin detect-now
// endpoint. When nil, the endpoint returns 501.
func (h *LibraryHandler) WithDetector(d IntroDetectorRunner) *LibraryHandler {
	h.detector = d
	return h
}

// WithAudit attaches an audit logger so admin actions on libraries
// (Create, Delete, Scan, DetectIntros) leave a forensic trail.
// Returns the handler for chaining.
func (h *LibraryHandler) WithAudit(a *audit.Logger) *LibraryHandler {
	h.audit = a
	return h
}

// List handles GET /api/v1/libraries.
func (h *LibraryHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	libs, err := h.svc.ListForUser(r.Context(), claims.UserID, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]LibraryResponse, len(libs))
	for i := range libs {
		out[i] = toLibraryResponse(&libs[i], claims.IsAdmin)
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// Get handles GET /api/v1/libraries/:id.
func (h *LibraryHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}

	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get library", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toLibraryResponse(lib, claims.IsAdmin))
}

// Create handles POST /api/v1/libraries.
func (h *LibraryHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name                    string        `json:"name"`
		Type                    string        `json:"type"`
		ScanPaths               []string      `json:"scan_paths"`
		Agent                   string        `json:"agent"`
		Language                string        `json:"language"`
		ScanInterval            time.Duration `json:"scan_interval_ns"`
		MetadataRefreshInterval time.Duration `json:"metadata_refresh_interval_ns"`
		IsPrivate               bool          `json:"is_private"`
		AutoGrantNewUsers       bool          `json:"auto_grant_new_users"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	if len(body.ScanPaths) == 0 {
		respond.BadRequest(w, r, "at least one scan path is required")
		return
	}
	for _, p := range body.ScanPaths {
		if strings.TrimSpace(p) == "" {
			respond.BadRequest(w, r, "scan paths must not be empty")
			return
		}
		if strings.Contains(p, "..") {
			respond.BadRequest(w, r, "scan paths must not contain '..'")
			return
		}
	}

	if body.Agent == "" {
		body.Agent = "tmdb"
	}
	if body.Language == "" {
		body.Language = "en"
	}
	if body.ScanInterval == 0 {
		body.ScanInterval = 24 * time.Hour
	}
	if body.MetadataRefreshInterval == 0 {
		body.MetadataRefreshInterval = 7 * 24 * time.Hour
	}

	// auto_grant_new_users only matters on private libraries — it's a
	// no-op on public ones (every user already sees them). Silently
	// drop the flag on public libraries so admin tooling can send the
	// pair without us writing meaningless rows.
	autoGrant := body.AutoGrantNewUsers && body.IsPrivate

	lib, err := h.svc.Create(r.Context(), library.CreateLibraryParams{
		Name:                    body.Name,
		Type:                    body.Type,
		Paths:                   body.ScanPaths,
		Agent:                   body.Agent,
		Lang:                    body.Language,
		ScanInterval:            body.ScanInterval,
		MetadataRefreshInterval: body.MetadataRefreshInterval,
		IsPrivate:               body.IsPrivate,
		AutoGrantNewUsers:       autoGrant,
	})
	if err != nil {
		var ve *library.ValidationError
		if errors.As(err, &ve) {
			respond.ValidationError(w, r, ve.Error())
			return
		}
		h.logger.ErrorContext(r.Context(), "create library", "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		var actor *uuid.UUID
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			a := claims.UserID
			actor = &a
		}
		h.audit.Log(r.Context(), actor, audit.ActionLibraryCreate, lib.ID.String(),
			map[string]any{"name": lib.Name, "type": lib.Type}, audit.ClientIP(r))
	}
	// Create is admin-only at the router; admins always see scan_paths.
	respond.Created(w, r, toLibraryResponse(lib, true))
}

// Update handles PATCH /api/v1/libraries/:id.
//
// Real partial-update semantics: each field omitted from the body keeps
// its current value. Previously the handler treated empty-string Name /
// nil ScanPaths as "set to empty," which (a) didn't match the documented
// PATCH contract and (b) caused the underlying UpdateLibrary SQL to fail
// validation with an unhelpful 500 when callers sent only the field they
// wanted to change. The fix fetches the existing row first and falls back
// to its values for any field the client didn't send. Unknown fields are
// rejected (DisallowUnknownFields) so a typo like `is-private` returns a
// clear 400 instead of silently being ignored.
func (h *LibraryHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}

	// Fetch existing first so we can merge partial updates into it.
	existing, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get library for update", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	// All fields are pointers so we can distinguish "not sent" from
	// "sent as empty" — the latter is a meaningful clear for some
	// fields (e.g. clearing the agent), the former must preserve.
	var body struct {
		Name                    *string        `json:"name,omitempty"`
		ScanPaths               *[]string      `json:"scan_paths,omitempty"`
		Agent                   *string        `json:"agent,omitempty"`
		Language                *string        `json:"language,omitempty"`
		ScanIntervalMinutes     *int           `json:"scan_interval_minutes,omitempty"`
		ScanInterval            *time.Duration `json:"scan_interval_ns,omitempty"`
		MetadataRefreshInterval *time.Duration `json:"metadata_refresh_interval_ns,omitempty"`
		IsPrivate               *bool          `json:"is_private,omitempty"`
		AutoGrantNewUsers       *bool          `json:"auto_grant_new_users,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body: "+err.Error())
		return
	}

	// Resolve scan interval: prefer minutes if provided, else explicit
	// duration, else fall back to existing (Library.ScanInterval is a
	// nullable pointer; UpdateLibraryParams.ScanInterval is a plain
	// duration with zero meaning "no override," so deref carefully).
	var scanInterval time.Duration
	if existing.ScanInterval != nil {
		scanInterval = *existing.ScanInterval
	}
	if body.ScanIntervalMinutes != nil {
		scanInterval = time.Duration(*body.ScanIntervalMinutes) * time.Minute
	} else if body.ScanInterval != nil {
		scanInterval = *body.ScanInterval
	}

	var metadataInterval time.Duration
	if existing.MetadataRefreshInterval != nil {
		metadataInterval = *existing.MetadataRefreshInterval
	}
	if body.MetadataRefreshInterval != nil {
		metadataInterval = *body.MetadataRefreshInterval
	}

	name := existing.Name
	if body.Name != nil {
		// Empty name still rejected by the service layer downstream;
		// only short-circuit non-nil values onto the patch.
		name = *body.Name
	}
	paths := existing.Paths
	if body.ScanPaths != nil {
		paths = *body.ScanPaths
	}
	agent := existing.Agent
	if body.Agent != nil {
		agent = *body.Agent
	}
	lang := existing.Lang
	if body.Language != nil {
		lang = *body.Language
	}

	lib, err := h.svc.Update(r.Context(), library.UpdateLibraryParams{
		ID:                      id,
		Name:                    name,
		Paths:                   paths,
		Agent:                   agent,
		Lang:                    lang,
		ScanInterval:            scanInterval,
		MetadataRefreshInterval: metadataInterval,
		IsPrivate:               body.IsPrivate,
		AutoGrantNewUsers:       body.AutoGrantNewUsers,
	})
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "update library", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	// Update is admin-only at the router; admins always see scan_paths.
	respond.Success(w, r, toLibraryResponse(lib, true))
}

// Delete handles DELETE /api/v1/libraries/:id.
func (h *LibraryHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "delete library", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		var actor *uuid.UUID
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			a := claims.UserID
			actor = &a
		}
		h.audit.Log(r.Context(), actor, audit.ActionLibraryDelete, id.String(), nil, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// Refresh handles POST /api/v1/libraries/:id/scan — triggers an on-demand scan.
func (h *LibraryHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}

	if err := h.svc.EnqueueScan(r.Context(), id); err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "enqueue scan", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		var actor *uuid.UUID
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			a := claims.UserID
			actor = &a
		}
		h.audit.Log(r.Context(), actor, audit.ActionLibraryScan, id.String(), nil, audit.ClientIP(r))
	}
	respond.NoContent(w)
}

// DetectIntros handles POST /api/v1/libraries/:id/detect-intros. Admin only.
// Fires the detector over every season in the library on a background
// goroutine and returns 202 Accepted — detection can take many minutes.
func (h *LibraryHandler) DetectIntros(w http.ResponseWriter, r *http.Request) {
	if h.detector == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "intro detector not available")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get library for detect-intros", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if lib.Type != "show" && lib.Type != "anime" {
		respond.BadRequest(w, r, "intro detection only applies to show / anime libraries")
		return
	}
	detectCtx := context.WithoutCancel(r.Context())
	go func() {
		if err := h.detector.DetectLibrary(detectCtx, id); err != nil {
			h.logger.Warn("admin detect-intros",
				"library_id", id, "err", err)
		}
	}()
	respond.JSON(w, r, http.StatusAccepted, map[string]any{
		"data": map[string]string{"status": "detection_started"},
	})
}

// Items handles GET /api/v1/libraries/:id/items.
func (h *LibraryHandler) Items(w http.ResponseWriter, r *http.Request) {
	if h.media == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "media listing not available")
		return
	}

	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}

	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}

	page := respond.ParsePagination(r, 50, 200)
	limit, offset := page.Limit, page.Offset

	// Parse filter/sort params.
	q := r.URL.Query()
	fp := media.FilterParams{
		Sort:    "title",
		SortAsc: true,
	}
	if g := q.Get("genre"); g != "" {
		fp.Genre = &g
	}
	if v, err := strconv.Atoi(q.Get("year_min")); err == nil {
		fp.YearMin = &v
	}
	if v, err := strconv.Atoi(q.Get("year_max")); err == nil {
		fp.YearMax = &v
	}
	if v, err := strconv.ParseFloat(q.Get("rating_min"), 64); err == nil {
		fp.RatingMin = &v
	}
	if s := q.Get("sort"); s != "" {
		fp.Sort = s
	}
	if q.Get("sort_dir") == "desc" {
		fp.SortAsc = false
	} else if q.Get("sort_dir") == "asc" {
		fp.SortAsc = true
	} else if fp.Sort == "rating" || fp.Sort == "created_at" || fp.Sort == "year" || fp.Sort == "taken_at" {
		fp.SortAsc = false // default desc for rating/date/year/taken
	}

	// Inject content rating ceiling from auth claims.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if rk := contentrating.MaxRatingRank(claims.MaxContentRating); rk != nil {
			fp.MaxRatingRank = rk
		}
	}

	hasFilter := fp.Genre != nil || fp.YearMin != nil || fp.YearMax != nil || fp.RatingMin != nil || fp.MaxRatingRank != nil || fp.Sort != "title" || !fp.SortAsc

	// `?type=` lets callers list a non-root item type within the library —
	// e.g. ?type=music_video on a music library returns the videos that
	// hang off artists, not the artists themselves. Each library type
	// defines its own allow-list to avoid leaking unrelated types via a
	// crafted query (e.g. asking for ?type=movie on a music library).
	rootType := rootItemType(lib.Type)
	if t := q.Get("type"); t != "" {
		if !validItemTypeForLibrary(lib.Type, t) {
			respond.BadRequest(w, r, "type "+t+" is not valid for "+lib.Type+" library")
			return
		}
		rootType = t
	}
	var items []media.Item
	var total int64
	if hasFilter {
		items, err = h.media.ListItemsFiltered(r.Context(), id, rootType, limit, offset, fp)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list media items filtered", "library_id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		total, _ = h.media.CountItemsFiltered(r.Context(), id, rootType, fp)
	} else {
		items, err = h.media.ListItems(r.Context(), id, rootType, limit, offset)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "list media items", "library_id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		total, _ = h.media.CountItems(r.Context(), id, rootType)
	}

	out := make([]MediaItemResponse, len(items))
	for i, item := range items {
		out[i] = MediaItemResponse{
			ID:            item.ID.String(),
			Title:         item.Title,
			Type:          item.Type,
			Year:          item.Year,
			Summary:       item.Summary,
			Rating:        item.Rating,
			DurationMS:    item.DurationMS,
			Genres:        item.Genres,
			PosterPath:    item.PosterPath,
			OriginalTitle: item.OriginalTitle,
			TakenAt:       item.OriginallyAvailableAt,
			CreatedAt:     item.CreatedAt,
			UpdatedAt:     item.UpdatedAt.UnixMilli(),
		}
	}
	respond.List(w, r, out, total, "")
}

// Genres handles GET /api/v1/libraries/:id/genres.
func (h *LibraryHandler) Genres(w http.ResponseWriter, r *http.Request) {
	if h.media == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "media listing not available")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}
	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	rows, err := h.media.ListGenresWithCounts(r.Context(), id, rootItemType(lib.Type))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list genres", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]GenreCountResponse, len(rows))
	for i, g := range rows {
		out[i] = GenreCountResponse{Name: g.Genre, Count: g.Count}
	}
	respond.Success(w, r, out)
}

// GenreCountResponse is one row of /libraries/{id}/genres.
type GenreCountResponse struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// EventCollectionResponse is one row of /libraries/{id}/event-collections.
type EventCollectionResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	PosterPath *string `json:"poster_path,omitempty"`
}

// EventCollections handles GET /api/v1/libraries/{id}/event-collections.
// Returns the auto-created event_folder collections for a home_video
// library (the home-video scanner upserts one per non-root subfolder).
// Surface for the library page's "Events" shelf.
func (h *LibraryHandler) EventCollections(w http.ResponseWriter, r *http.Request) {
	if h.media == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "media listing not available")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}
	rows, err := h.media.ListEventCollectionsForLibrary(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list event collections", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]EventCollectionResponse, len(rows))
	for i, c := range rows {
		out[i] = EventCollectionResponse{
			ID:         c.ID.String(),
			Name:       c.Name,
			PosterPath: c.PosterPath,
		}
	}
	respond.Success(w, r, out)
}

// YearCountResponse is one row of /libraries/{id}/years.
type YearCountResponse struct {
	Year  int32 `json:"year"`
	Count int64 `json:"count"`
}

// Years handles GET /api/v1/libraries/:id/years.
func (h *LibraryHandler) Years(w http.ResponseWriter, r *http.Request) {
	if h.media == nil {
		respond.Error(w, r, http.StatusNotImplemented, "NOT_IMPLEMENTED", "media listing not available")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return
	}
	ok, err := h.svc.CanAccessLibrary(r.Context(), claims.UserID, id, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !ok {
		respond.NotFound(w, r)
		return
	}
	lib, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	rows, err := h.media.ListYearsWithCounts(r.Context(), id, rootItemType(lib.Type))
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list years", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]YearCountResponse, len(rows))
	for i, y := range rows {
		out[i] = YearCountResponse{Year: y.Year, Count: y.Count}
	}
	respond.Success(w, r, out)
}

func parseUUID(r *http.Request, param string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, param))
}

// rootItemType maps a library type to the DB item type used for top-level items.
// Music libraries store artists as top-level items; show libraries store shows.
func rootItemType(libraryType string) string {
	switch libraryType {
	case "music":
		return "artist"
	case "show", "anime":
		// Anime libraries share the show → season → episode shape
		// with `show` libraries. The library type only flips which
		// metadata agent the enricher prefers, not the hierarchy.
		return "show"
	case "photo":
		return "photo"
	case "audiobook":
		// Audiobook libraries surface authors at the top level —
		// drilling in goes book_author → (book_series →) audiobook
		// → audiobook_chapter, mirroring the music artist → album
		// → track shape. Migration 00069 backfills authors from the
		// previous flat-grid rows.
		return "book_author"
	default:
		return libraryType // "movie" → "movie"
	}
}

// validItemTypeForLibrary returns true when itemType is a known child of
// libraryType's hierarchy. Backs the `?type=` override on the items
// endpoint — admin/UI clients can ask for a specific child level (music
// videos within a music library, podcast episodes within a podcast
// library, etc.) without enumerating an unrelated type via a crafted
// query.
func validItemTypeForLibrary(libraryType, itemType string) bool {
	switch libraryType {
	case "music":
		switch itemType {
		case "artist", "album", "track", "music_video":
			return true
		}
	case "show", "anime":
		switch itemType {
		case "show", "season", "episode":
			return true
		}
	case "movie":
		return itemType == "movie"
	case "photo":
		return itemType == "photo"
	case "audiobook":
		// book_author / book_series are the hierarchy parents above
		// an audiobook — the library grid lists book_author at the
		// top level (rootItemType), drilling renders the children of
		// each. audiobook_chapter is the leaf under a multi-file book.
		// All four types must accept through this check so detail /
		// children fetches don't 404 on any node of the hierarchy.
		switch itemType {
		case "book_author", "book_series", "audiobook", "audiobook_chapter":
			return true
		}
	case "podcast":
		switch itemType {
		case "podcast", "podcast_episode":
			return true
		}
	case "home_video":
		return itemType == "home_video"
	case "book":
		return itemType == "book"
	case "dvr":
		return itemType == "dvr"
	}
	return false
}
