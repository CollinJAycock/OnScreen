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
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
)

// LibraryResponse is the JSON shape for a library in the v1 API.
type LibraryResponse struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Type                string   `json:"type"`
	ScanPaths           []string `json:"scan_paths"`
	Agent               string   `json:"agent"`
	Language            string   `json:"language"`
	ScanIntervalMinutes *int     `json:"scan_interval_minutes,omitempty"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

func toLibraryResponse(lib *library.Library) LibraryResponse {
	paths := lib.Paths
	if paths == nil {
		paths = []string{}
	}
	r := LibraryResponse{
		ID:        lib.ID.String(),
		Name:      lib.Name,
		Type:      lib.Type,
		ScanPaths: paths,
		Agent:     lib.Agent,
		Language:  lib.Lang,
		CreatedAt: lib.CreatedAt.Format(time.RFC3339),
		UpdatedAt: lib.UpdatedAt.Format(time.RFC3339),
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
}

// MediaItemResponse is the JSON shape for a media item in the v1 API.
type MediaItemResponse struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Year       *int      `json:"year,omitempty"`
	Summary    *string   `json:"summary,omitempty"`
	Rating     *float64  `json:"rating,omitempty"`
	DurationMS *int64    `json:"duration_ms,omitempty"`
	Genres     []string  `json:"genres,omitempty"`
	PosterPath *string   `json:"poster_path,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  int64     `json:"updated_at"`
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
		out[i] = toLibraryResponse(&libs[i])
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
	respond.Success(w, r, toLibraryResponse(lib))
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

	lib, err := h.svc.Create(r.Context(), library.CreateLibraryParams{
		Name:                    body.Name,
		Type:                    body.Type,
		Paths:                   body.ScanPaths,
		Agent:                   body.Agent,
		Lang:                    body.Language,
		ScanInterval:            body.ScanInterval,
		MetadataRefreshInterval: body.MetadataRefreshInterval,
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
	respond.Created(w, r, toLibraryResponse(lib))
}

// Update handles PATCH /api/v1/libraries/:id.
func (h *LibraryHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return
	}

	var body struct {
		Name                    string        `json:"name"`
		ScanPaths               []string      `json:"scan_paths"`
		Agent                   string        `json:"agent"`
		Language                string        `json:"language"`
		ScanIntervalMinutes     *int          `json:"scan_interval_minutes"`
		ScanInterval            time.Duration `json:"scan_interval_ns"`
		MetadataRefreshInterval time.Duration `json:"metadata_refresh_interval_ns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	scanInterval := body.ScanInterval
	if body.ScanIntervalMinutes != nil {
		scanInterval = time.Duration(*body.ScanIntervalMinutes) * time.Minute
	}

	lib, err := h.svc.Update(r.Context(), library.UpdateLibraryParams{
		ID:                      id,
		Name:                    body.Name,
		Paths:                   body.ScanPaths,
		Agent:                   body.Agent,
		Lang:                    body.Language,
		ScanInterval:            scanInterval,
		MetadataRefreshInterval: body.MetadataRefreshInterval,
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
	respond.Success(w, r, toLibraryResponse(lib))
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
	if lib.Type != "show" {
		respond.BadRequest(w, r, "intro detection only applies to show libraries")
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

	const defaultLimit = 50
	limit := int32(defaultLimit)
	offset := int32(0)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32); err == nil && v > 0 {
		limit = int32(v)
	}
	if v, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 32); err == nil && v >= 0 {
		offset = int32(v)
	}

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
	} else if fp.Sort == "rating" || fp.Sort == "created_at" || fp.Sort == "year" {
		fp.SortAsc = false // default desc for rating/date/year
	}

	// Inject content rating ceiling from auth claims.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		if rk := contentrating.MaxRatingRank(claims.MaxContentRating); rk != nil {
			fp.MaxRatingRank = rk
		}
	}

	hasFilter := fp.Genre != nil || fp.YearMin != nil || fp.YearMax != nil || fp.RatingMin != nil || fp.MaxRatingRank != nil || fp.Sort != "title" || !fp.SortAsc

	rootType := rootItemType(lib.Type)
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
			ID:         item.ID.String(),
			Title:      item.Title,
			Type:       item.Type,
			Year:       item.Year,
			Summary:    item.Summary,
			Rating:     item.Rating,
			DurationMS: item.DurationMS,
			Genres:     item.Genres,
			PosterPath: item.PosterPath,
			CreatedAt:  item.CreatedAt,
			UpdatedAt:  item.UpdatedAt.UnixMilli(),
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
	genres, err := h.media.ListDistinctGenres(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list genres", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, genres)
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
	case "show":
		return "show"
	case "photo":
		return "photo"
	default:
		return libraryType // "movie" → "movie"
	}
}
