package v1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/artwork"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/itemlabel"
	"github.com/onscreen/onscreen/internal/photoimage"
	"github.com/onscreen/onscreen/internal/scanner"
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
	ListPhotoTimeline(ctx context.Context, libraryID uuid.UUID, maxRatingRank *int, labels itemlabel.Filter) ([]media.PhotoTimelineBucket, error)
	ListPhotoMapPoints(ctx context.Context, p media.ListPhotoMapPointsParams) ([]media.PhotoMapPoint, error)
	CountPhotoMapPoints(ctx context.Context, libraryID uuid.UUID, maxRatingRank *int, labels itemlabel.Filter) (int64, error)
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

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *PhotosHandler) LibraryAccessWired() bool { return h.access != nil }

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
//
// Every photo listing (List, Timeline, Map, Search) applies the caller's
// content-rating ceiling in SQL, the same gate Image enforces on the bytes. A
// restricted profile used to be refused the image yet handed the photo's title,
// EXIF and GPS coordinates by these endpoints.
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
		LibraryID:     libID,
		From:          from,
		To:            to,
		Limit:         limit,
		Offset:        offset,
		MaxRatingRank: callerRatingRank(r),
		Labels:        callerLabels(r),
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

	rows, err := h.media.ListPhotoTimeline(r.Context(), libID, callerRatingRank(r), callerLabels(r))
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
		LibraryID:     libID,
		MinLat:        minLat,
		MaxLat:        maxLat,
		MinLon:        minLon,
		MaxLon:        maxLon,
		Limit:         limit,
		MaxRatingRank: callerRatingRank(r),
		Labels:        callerLabels(r),
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list photo map points", "library_id", libID, "err", err)
		respond.InternalError(w, r)
		return
	}
	total, err := h.media.CountPhotoMapPoints(r.Context(), libID, callerRatingRank(r), callerLabels(r))
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
	// Content-rating ceiling, matching every other item byte surface. The
	// library ACL alone let a restricted profile pull the image bytes of an
	// over-ceiling item by direct URL. 404 (not 403) to match this handler's
	// existing shape — access denial stays indistinguishable from absence.
	if claims := middleware.ClaimsFromContext(r.Context()); viewerRestricted(claims) {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !itemPermitted(r.Context(), claims, item.ID, cr) {
			respond.NotFound(w, r)
			return
		}
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

	// Snap every cache-key input to a small fixed set.
	//
	// All four of these (w, h, fit, q) are hashed into the resize cache key,
	// and the cache is never evicted — no scheduler task touches this
	// directory. Clamping w/h to 0..4096 and q to 1..95 bounded each value but
	// left the KEY SPACE at roughly 4097 x 4097 x 2 x 95 ≈ 3.2e9 entries per
	// source image, every one of which is a decode + resize + encode + write
	// on first request. A read grant was therefore an unbounded, unevictable
	// write primitive against a volume shared with subtitles, live TV and DVR.
	//
	// This is the same defect, and the same fix, as the /artwork route:
	// artwork.SnapDim rounds up to a ladder so real traffic maps to itself and
	// the key space is bounded by ladder-size squared. Quality is quantised
	// rather than dropped so the few callers that ask for a specific value
	// still get a sensible one. Rounding UP means a client never receives
	// fewer pixels than it asked for.
	q := r.URL.Query()
	width := artwork.SnapDim(clampDim(parseInt(q.Get("w"), 0)))
	height := artwork.SnapDim(clampDim(parseInt(q.Get("h"), 0)))
	fit := photoimage.Fit(q.Get("fit"))
	if fit != photoimage.FitCover {
		fit = photoimage.FitContain
	}
	quality := snapQuality(parseInt(q.Get("q"), defaultImageQty))

	// A RAW + JPEG pair shows its JPEG; a RAW alone shows its embedded
	// preview (photoimage decodes it). Audiobooks have one file.
	source := files[0].FilePath
	if item.Type == "photo" {
		source = photoDisplayFile(files)
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600, immutable")

	if err := h.images.Serve(r.Context(), w, source, photoimage.Options{
		Width:   width,
		Height:  height,
		Fit:     fit,
		Quality: quality,
	}); err != nil {
		// Skip the noisy "broken pipe" / "context canceled" log line
		// when the client just navigated away mid-fetch — a TV
		// remote scrolled past the photo, the activity went to the
		// background, etc. Drop those to debug; real server-side
		// failures still log at warn so they show up in /admin/logs.
		if respond.IsClientGone(err) {
			h.logger.DebugContext(r.Context(), "serve photo image: client gone",
				"id", id, "path", source, "err", err)
		} else {
			h.logger.WarnContext(r.Context(), "serve photo image",
				"id", id, "path", source, "err", err)
		}
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

// photoDisplayFile is the file a photo item shows: the JPEG of a RAW + JPEG
// pair (see scanner.PhotoDisplayPath). The RAW stays the original download.
func photoDisplayFile(files []media.File) string {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.FilePath
	}
	return scanner.PhotoDisplayPath(paths)
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
	// On 64-bit platforms int is 64 bits, so a value like "9999999999" would
	// silently wrap when narrowed to int32 (possibly to a negative limit/offset
	// the SQL layer then mishandles). Clamp to the int32 ceiling instead.
	if n > math.MaxInt32 {
		return math.MaxInt32
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

func clampDim(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxImageDimension {
		return maxImageDimension
	}
	return n
}

// qualityLadder is the set of JPEG quality values the photo route will encode
// at. Quality is part of the resize cache key, so accepting all 95 legal
// values multiplied the never-evicted key space by 95 for no visible benefit —
// the difference between q=82 and q=83 is not perceptible, but it is two
// separate files on disk forever.
var qualityLadder = [...]int{50, 65, 75, 82, 90, 95}

// snapQuality rounds a requested JPEG quality UP to the next ladder value, so
// a caller never gets fewer bits than they asked for. Out-of-range and
// unparseable values fall back to the default.
func snapQuality(qty int) int {
	if qty < 1 || qty > maxImageQty {
		return defaultImageQty
	}
	for _, v := range qualityLadder {
		if qty <= v {
			return v
		}
	}
	return qualityLadder[len(qualityLadder)-1]
}
