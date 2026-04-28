package v1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/photoimage"
)

// Per-request caps on the on-the-fly resize endpoint. Without these a
// caller could ask for a 32K-wide derivative and burn server CPU + cache
// disk for nothing useful. 4096 covers a 4K viewer; quality 95 is the
// upper bound for "visually lossless" JPEG.
const (
	maxImageDimension = 4096
	defaultImageQty   = 85
	maxImageQty       = 95
)

// Map-endpoint caps. The default of 5000 markers handles the common
// "few-thousand-photo personal library" view in one round-trip while
// staying well under the wire budget where Leaflet/MapLibre with a
// supercluster index still feels snappy. The hard ceiling protects
// against pathological clients that send `limit=999999` to defeat
// pagination — beyond ~25k unclustered markers the browser starts to
// stutter on pan/zoom regardless of what the server does.
const (
	defaultMapPointLimit = 5000
	maxMapPointLimit     = 25000
)

// PhotoMediaService is the slice of the media domain the photos handler
// needs. Kept narrow so tests can stub it without dragging in the full
// media surface.
type PhotoMediaService interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListPhotos(ctx context.Context, p media.ListPhotosParams) ([]media.PhotoListItem, error)
	CountPhotos(ctx context.Context, p media.ListPhotosParams) (int64, error)
	ListPhotoTimeline(ctx context.Context, libraryID uuid.UUID) ([]media.PhotoTimelineBucket, error)
	ListPhotoMapPoints(ctx context.Context, p media.ListPhotoMapPointsParams) ([]media.PhotoMapPoint, error)
	CountPhotoMapPoints(ctx context.Context, libraryID uuid.UUID) (int64, error)
	SearchPhotosByExif(ctx context.Context, p media.SearchPhotosByExifParams) ([]media.PhotoSearchResult, error)
	CountPhotosByExif(ctx context.Context, p media.SearchPhotosByExifParams) (int64, error)
}

// PhotosHandler serves the photo browse list, the timeline sidebar
// aggregation, and the on-demand resize/orient image endpoint.
type PhotosHandler struct {
	media  PhotoMediaService
	images *photoimage.Server
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewPhotosHandler wires the dependencies. images is required (without it
// /items/{id}/image returns 503).
func NewPhotosHandler(m PhotoMediaService, images *photoimage.Server, logger *slog.Logger) *PhotosHandler {
	return &PhotosHandler{media: m, images: images, logger: logger}
}

// WithLibraryAccess attaches the per-library ACL checker. When nil, all
// items pass — matches the pre-ACL default elsewhere in this package.
func (h *PhotosHandler) WithLibraryAccess(a LibraryAccessChecker) *PhotosHandler {
	h.access = a
	return h
}

// PhotoListItemResponse is the JSON shape one photo takes in the list
// endpoint. Camera fields and dimensions are included so the grid can
// render width/height-aware tiles without a second EXIF round-trip.
type PhotoListItemResponse struct {
	ID          uuid.UUID  `json:"id"`
	LibraryID   uuid.UUID  `json:"library_id"`
	Title       string     `json:"title"`
	PosterPath  *string    `json:"poster_path,omitempty"`
	TakenAt     *time.Time `json:"taken_at,omitempty"`
	CameraMake  *string    `json:"camera_make,omitempty"`
	CameraModel *string    `json:"camera_model,omitempty"`
	Width       *int32     `json:"width,omitempty"`
	Height      *int32     `json:"height,omitempty"`
	Orientation *int32     `json:"orientation,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// PhotoTimelineBucketResponse is the (year, month, count) row for the
// sticky-section timeline sidebar.
type PhotoTimelineBucketResponse struct {
	Year  int32 `json:"year"`
	Month int32 `json:"month"`
	Count int64 `json:"count"`
}

// List handles GET /api/v1/photos?library_id=...&from=...&to=...&limit=&offset=.
// from/to are RFC3339 timestamps and are inclusive. Without library_id we
// 400 — there is no implicit "all libraries" scope because library access
// is checked per-library.
func (h *PhotosHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	libIDStr := q.Get("library_id")
	if libIDStr == "" {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	libID, err := uuid.Parse(libIDStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return
	}
	if !h.checkLibraryAccess(w, r, libID) {
		return
	}

	limit := parseInt32(q.Get("limit"), 100)
	if limit > 500 {
		limit = 500
	}
	offset := parseInt32(q.Get("offset"), 0)

	from, ferr := parseTimePtr(q.Get("from"))
	if ferr != nil {
		respond.BadRequest(w, r, "invalid from timestamp")
		return
	}
	to, terr := parseTimePtr(q.Get("to"))
	if terr != nil {
		respond.BadRequest(w, r, "invalid to timestamp")
		return
	}

	params := media.ListPhotosParams{
		LibraryID: libID,
		From:      from,
		To:        to,
		Limit:     limit,
		Offset:    offset,
	}

	rows, err := h.media.ListPhotos(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photos", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.media.CountPhotos(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count photos", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]PhotoListItemResponse, len(rows))
	for i, r := range rows {
		out[i] = PhotoListItemResponse{
			ID:          r.ID,
			LibraryID:   r.LibraryID,
			Title:       r.Title,
			PosterPath:  r.PosterPath,
			TakenAt:     r.TakenAt,
			CameraMake:  r.CameraMake,
			CameraModel: r.CameraModel,
			Width:       r.Width,
			Height:      r.Height,
			Orientation: r.Orientation,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		}
	}
	respond.List(w, r, out, total, "")
}

// Timeline handles GET /api/v1/photos/timeline?library_id=...
// Returns the full set of (year, month, count) buckets so the client can
// render a sticky-section sidebar in the grid.
func (h *PhotosHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	libIDStr := r.URL.Query().Get("library_id")
	if libIDStr == "" {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	libID, err := uuid.Parse(libIDStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return
	}
	if !h.checkLibraryAccess(w, r, libID) {
		return
	}

	rows, err := h.media.ListPhotoTimeline(r.Context(), libID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo timeline", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]PhotoTimelineBucketResponse, len(rows))
	for i, r := range rows {
		out[i] = PhotoTimelineBucketResponse{Year: r.Year, Month: r.Month, Count: r.Count}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// PhotoMapPointResponse is one geotagged photo in the /photos/map
// response. Lat/lon are doubles rather than strings so JSON consumers
// can pass them directly to mapping libraries.
type PhotoMapPointResponse struct {
	ID         uuid.UUID  `json:"id"`
	LibraryID  uuid.UUID  `json:"library_id"`
	Title      string     `json:"title"`
	PosterPath *string    `json:"poster_path,omitempty"`
	Lat        float64    `json:"lat"`
	Lon        float64    `json:"lon"`
	TakenAt    *time.Time `json:"taken_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Map handles GET /api/v1/photos/map?library_id=&min_lat=&max_lat=&min_lon=&max_lon=&limit=.
// Returns geotagged photo points for client-side map rendering. The bbox
// args are independently optional — passing none returns the whole
// library (capped at limit). The response envelope's `total` is the
// library-wide geotagged-photo count (ignoring bbox), so the UI can show
// "showing N of M — zoom in to see more" when truncated.
//
// Antimeridian crossings (e.g. min_lon=170, max_lon=-170) must be
// handled client-side as two separate requests; the SQL filter is a
// straight BETWEEN.
func (h *PhotosHandler) Map(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	libIDStr := q.Get("library_id")
	if libIDStr == "" {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	libID, err := uuid.Parse(libIDStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return
	}
	if !h.checkLibraryAccess(w, r, libID) {
		return
	}

	minLat, err := parseLatLonPtr(q.Get("min_lat"), -90, 90)
	if err != nil {
		respond.BadRequest(w, r, "invalid min_lat")
		return
	}
	maxLat, err := parseLatLonPtr(q.Get("max_lat"), -90, 90)
	if err != nil {
		respond.BadRequest(w, r, "invalid max_lat")
		return
	}
	minLon, err := parseLatLonPtr(q.Get("min_lon"), -180, 180)
	if err != nil {
		respond.BadRequest(w, r, "invalid min_lon")
		return
	}
	maxLon, err := parseLatLonPtr(q.Get("max_lon"), -180, 180)
	if err != nil {
		respond.BadRequest(w, r, "invalid max_lon")
		return
	}

	limit := parseInt32(q.Get("limit"), defaultMapPointLimit)
	if limit <= 0 || limit > maxMapPointLimit {
		limit = defaultMapPointLimit
	}

	rows, err := h.media.ListPhotoMapPoints(r.Context(), media.ListPhotoMapPointsParams{
		LibraryID: libID,
		MinLat:    minLat,
		MaxLat:    maxLat,
		MinLon:    minLon,
		MaxLon:    maxLon,
		Limit:     limit,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo map points", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.media.CountPhotoMapPoints(r.Context(), libID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count photo map points", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]PhotoMapPointResponse, len(rows))
	for i, p := range rows {
		out[i] = PhotoMapPointResponse{
			ID:         p.ID,
			LibraryID:  p.LibraryID,
			Title:      p.Title,
			PosterPath: p.PosterPath,
			Lat:        p.Lat,
			Lon:        p.Lon,
			TakenAt:    p.TakenAt,
			CreatedAt:  p.CreatedAt,
		}
	}
	respond.List(w, r, out, total, "")
}

// PhotoSearchResultResponse is one row in the EXIF search response. Carries
// the EXIF fields the search filtered on so the result UI can render
// "matched on f/2.8, ISO 6400" hints alongside the thumbnail without a
// follow-up `/items/{id}/exif` round-trip per row.
type PhotoSearchResultResponse struct {
	ID            uuid.UUID  `json:"id"`
	LibraryID     uuid.UUID  `json:"library_id"`
	Title         string     `json:"title"`
	PosterPath    *string    `json:"poster_path,omitempty"`
	TakenAt       *time.Time `json:"taken_at,omitempty"`
	CameraMake    *string    `json:"camera_make,omitempty"`
	CameraModel   *string    `json:"camera_model,omitempty"`
	LensModel     *string    `json:"lens_model,omitempty"`
	FocalLengthMM *float64   `json:"focal_length_mm,omitempty"`
	Aperture      *float64   `json:"aperture,omitempty"`
	ISO           *int32     `json:"iso,omitempty"`
	Width         *int32     `json:"width,omitempty"`
	Height        *int32     `json:"height,omitempty"`
	Orientation   *int32     `json:"orientation,omitempty"`
	GPSLat        *float64   `json:"gps_lat,omitempty"`
	GPSLon        *float64   `json:"gps_lon,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Search handles GET /api/v1/photos/search with EXIF-driven filters:
//
//	library_id   (required)
//	camera_make, camera_model, lens_model  — case-insensitive substring
//	aperture_min, aperture_max             — f-stop range, inclusive
//	iso_min, iso_max                       — ISO range, inclusive
//	focal_min, focal_max                   — focal length mm range, inclusive
//	from, to                               — RFC3339 taken_at range
//	has_gps                                — "true"/"false" tri-state; absent = don't care
//	limit, offset                          — pagination (limit clamps to 500)
//
// All filters are AND-combined. The search is INNER-JOINed against
// photo_metadata, so photos without an EXIF row (screenshots, PNGs)
// never match — `/photos` is the right endpoint for those.
func (h *PhotosHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	libIDStr := q.Get("library_id")
	if libIDStr == "" {
		respond.BadRequest(w, r, "library_id is required")
		return
	}
	libID, err := uuid.Parse(libIDStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid library_id")
		return
	}
	if !h.checkLibraryAccess(w, r, libID) {
		return
	}

	limit := parseInt32(q.Get("limit"), 100)
	if limit > 500 {
		limit = 500
	}
	offset := parseInt32(q.Get("offset"), 0)

	from, ferr := parseTimePtr(q.Get("from"))
	if ferr != nil {
		respond.BadRequest(w, r, "invalid from timestamp")
		return
	}
	to, terr := parseTimePtr(q.Get("to"))
	if terr != nil {
		respond.BadRequest(w, r, "invalid to timestamp")
		return
	}

	apertureMin, err := parseFloatPtr(q.Get("aperture_min"))
	if err != nil {
		respond.BadRequest(w, r, "invalid aperture_min")
		return
	}
	apertureMax, err := parseFloatPtr(q.Get("aperture_max"))
	if err != nil {
		respond.BadRequest(w, r, "invalid aperture_max")
		return
	}
	isoMin, err := parseInt32Ptr(q.Get("iso_min"))
	if err != nil {
		respond.BadRequest(w, r, "invalid iso_min")
		return
	}
	isoMax, err := parseInt32Ptr(q.Get("iso_max"))
	if err != nil {
		respond.BadRequest(w, r, "invalid iso_max")
		return
	}
	focalMin, err := parseFloatPtr(q.Get("focal_min"))
	if err != nil {
		respond.BadRequest(w, r, "invalid focal_min")
		return
	}
	focalMax, err := parseFloatPtr(q.Get("focal_max"))
	if err != nil {
		respond.BadRequest(w, r, "invalid focal_max")
		return
	}
	hasGPS, err := parseBoolPtr(q.Get("has_gps"))
	if err != nil {
		respond.BadRequest(w, r, "invalid has_gps")
		return
	}

	params := media.SearchPhotosByExifParams{
		LibraryID:   libID,
		CameraMake:  strPtrIfNonEmpty(q.Get("camera_make")),
		CameraModel: strPtrIfNonEmpty(q.Get("camera_model")),
		LensModel:   strPtrIfNonEmpty(q.Get("lens_model")),
		ApertureMin: apertureMin,
		ApertureMax: apertureMax,
		ISOMin:      isoMin,
		ISOMax:      isoMax,
		FocalMin:    focalMin,
		FocalMax:    focalMax,
		From:        from,
		To:          to,
		HasGPS:      hasGPS,
		Limit:       limit,
		Offset:      offset,
	}

	rows, err := h.media.SearchPhotosByExif(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "search photos by exif", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.media.CountPhotosByExif(r.Context(), params)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "count photos by exif", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]PhotoSearchResultResponse, len(rows))
	for i, p := range rows {
		out[i] = PhotoSearchResultResponse{
			ID:            p.ID,
			LibraryID:     p.LibraryID,
			Title:         p.Title,
			PosterPath:    p.PosterPath,
			TakenAt:       p.TakenAt,
			CameraMake:    p.CameraMake,
			CameraModel:   p.CameraModel,
			LensModel:     p.LensModel,
			FocalLengthMM: p.FocalLengthMM,
			Aperture:      p.Aperture,
			ISO:           p.ISO,
			Width:         p.Width,
			Height:        p.Height,
			Orientation:   p.Orientation,
			GPSLat:        p.GPSLat,
			GPSLon:        p.GPSLon,
			CreatedAt:     p.CreatedAt,
			UpdatedAt:     p.UpdatedAt,
		}
	}
	respond.List(w, r, out, total, "")
}

// Image handles GET /api/v1/items/{id}/image?w=...&h=...&fit=...&q=....
// Resolves the item's primary file and pipes it through the on-demand
// resize cache. Output is JPEG; HEIC sources are decoded by ffmpeg first.
//
// Cache responses are aggressively cacheable on clients (immutable for an
// hour) since the cache key embeds dimensions — a different size becomes
// a different URL.
func (h *PhotosHandler) Image(w http.ResponseWriter, r *http.Request) {
	if h.images == nil {
		respond.Error(w, r, http.StatusServiceUnavailable, "IMAGE_SERVER_UNAVAILABLE", "image server not configured")
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for image", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	// The image endpoint serves photos directly from their source file
	// and audiobook covers via ffmpeg-extracted embedded artwork.
	// Movies / shows / episodes / music keep using poster_path on the
	// item — those have proper /artwork/ assets from the metadata
	// agent, so routing through here would just add an ffmpeg hop.
	// 404 for other types keeps URL-fishers from distinguishing
	// "wrong type" from "doesn't exist."
	if item.Type != "photo" && item.Type != "audiobook" {
		respond.NotFound(w, r)
		return
	}
	files, err := h.media.GetFiles(r.Context(), id)
	if err != nil || len(files) == 0 {
		respond.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	width := clampDim(parseInt(q.Get("w"), 0))
	height := clampDim(parseInt(q.Get("h"), 0))
	fit := photoimage.Fit(q.Get("fit"))
	if fit != photoimage.FitCover {
		fit = photoimage.FitContain
	}
	quality := parseInt(q.Get("q"), defaultImageQty)
	if quality < 1 || quality > maxImageQty {
		quality = defaultImageQty
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")

	if err := h.images.Serve(r.Context(), w, files[0].FilePath, photoimage.Options{
		Width:   width,
		Height:  height,
		Fit:     fit,
		Quality: quality,
	}); err != nil {
		h.logger.WarnContext(r.Context(), "serve photo image",
			"id", id, "path", files[0].FilePath, "err", err)
		// Headers may already be flushed; best we can do is stop writing.
		return
	}
}

// checkLibraryAccess mirrors ItemHandler.checkLibraryAccess so the photos
// endpoints enforce the same per-library grants without depending on the
// items handler.
func (h *PhotosHandler) checkLibraryAccess(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
	if h.access == nil {
		return true
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Forbidden(w, r)
		return false
	}
	ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, libraryID, claims.IsAdmin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "check library access", "library_id", libraryID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	if !ok {
		respond.NotFound(w, r)
		return false
	}
	return true
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseInt32(s string, def int32) int32 {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return def
	}
	return int32(n)
}

func parseTimePtr(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// parseLatLonPtr parses an optional latitude/longitude query string and
// validates it sits inside [min, max]. Empty input is treated as "absent"
// (returns nil, nil) so callers can distinguish "no bound" from "bad
// input." Out-of-range values are rejected so the SQL bbox filter never
// runs with garbage like lat=200.
func parseLatLonPtr(s string, min, max float64) (*float64, error) {
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	if v < min || v > max {
		return nil, fmt.Errorf("out of range")
	}
	return &v, nil
}

// parseFloatPtr returns nil for empty input so callers can pass it as
// "filter not set" to the SQL query, and bubbles parse errors so the
// handler can 400 on malformed input rather than silently dropping it.
func parseFloatPtr(s string) (*float64, error) {
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func parseInt32Ptr(s string) (*int32, error) {
	if s == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, err
	}
	v := int32(n)
	return &v, nil
}

// parseBoolPtr is tri-state: "" → nil (don't filter), "true"/"false" →
// pointer to the parsed bool, anything else → error so we can 400.
func parseBoolPtr(s string) (*bool, error) {
	if s == "" {
		return nil, nil
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func strPtrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func clampDim(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxImageDimension {
		return maxImageDimension
	}
	return n
}
