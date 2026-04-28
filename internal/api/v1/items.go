package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/intromarker"
	"github.com/onscreen/onscreen/internal/notification"
	"github.com/onscreen/onscreen/internal/scanner"
	"github.com/onscreen/onscreen/internal/streaming"
)

// ItemMediaService defines the media domain operations the items handler needs.
type ItemMediaService interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error)
	GetPhotoMetadata(ctx context.Context, itemID uuid.UUID) (*media.PhotoMetadata, error)
}

// ItemEnricher re-runs metadata enrichment for a single item on demand.
type ItemEnricher interface {
	EnrichItem(ctx context.Context, itemID uuid.UUID) error
	MatchItem(ctx context.Context, itemID uuid.UUID, tmdbID int) error
}

// ItemMatchSearcher searches for metadata candidates for manual matching.
type ItemMatchSearcher interface {
	SearchTVCandidates(ctx context.Context, query string) ([]MatchCandidate, error)
	SearchMovieCandidates(ctx context.Context, query string) ([]MatchCandidate, error)
}

// MatchCandidate is a TMDB search result shown to the user for manual selection.
type MatchCandidate struct {
	TMDBID    int     `json:"tmdb_id"`
	Title     string  `json:"title"`
	Year      int     `json:"year,omitempty"`
	Summary   string  `json:"summary,omitempty"`
	PosterURL string  `json:"poster_url,omitempty"`
	Rating    float64 `json:"rating,omitempty"`
}

// ItemWebhookDispatcher fires webhook events for media playback state changes.
type ItemWebhookDispatcher interface {
	Dispatch(eventType string, userID, mediaID uuid.UUID)
}

// ItemWatchService defines the watch event operations the items handler needs.
type ItemWatchService interface {
	GetState(ctx context.Context, userID, mediaID uuid.UUID) (watchevent.WatchState, error)
	Record(ctx context.Context, p watchevent.RecordParams) error
}

// ItemSessionCleaner manages transcode sessions on behalf of the items handler.
type ItemSessionCleaner interface {
	UpdatePositionByMedia(ctx context.Context, mediaItemID uuid.UUID, positionMS int64) error
	DeleteByMedia(ctx context.Context, mediaItemID uuid.UUID) error
}

// ItemFavoriteChecker reports whether a media item is favorited by a given user.
type ItemFavoriteChecker interface {
	IsFavorite(ctx context.Context, userID, mediaID uuid.UUID) (bool, error)
}

// ItemMarkerService reads and writes intro/credits markers for an item.
// Only episodes carry markers; movies and containers return an empty list.
type ItemMarkerService interface {
	List(ctx context.Context, mediaItemID uuid.UUID) ([]intromarker.Marker, error)
	Upsert(ctx context.Context, mediaItemID uuid.UUID, kind string, startMS, endMS int64) (intromarker.Marker, error)
	Delete(ctx context.Context, mediaItemID uuid.UUID, kind string) error
}

// LibraryAccessChecker reports whether a user can access a given library.
// Kept as a small interface so handlers don't depend on the full library service.
type LibraryAccessChecker interface {
	CanAccessLibrary(ctx context.Context, userID, libraryID uuid.UUID, isAdmin bool) (bool, error)
	AllowedLibraryIDs(ctx context.Context, userID uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error)
}

// ItemHandler handles /api/v1/items.
type ItemHandler struct {
	media     ItemMediaService
	watch     ItemWatchService
	sessions  ItemSessionCleaner
	enricher  ItemEnricher
	matcher   ItemMatchSearcher
	webhooks  ItemWebhookDispatcher
	favorites ItemFavoriteChecker
	markers   ItemMarkerService
	access    LibraryAccessChecker
	subs      ExternalSubLister
	tracker   *streaming.Tracker
	sync      *notification.Broker
	audit     *audit.Logger
	logger    *slog.Logger
}

// ExternalSubLister returns the saved external subtitle rows for a media file.
// When nil on the handler, the Get response omits the field.
type ExternalSubLister interface {
	List(ctx context.Context, fileID uuid.UUID) ([]gen.ExternalSubtitle, error)
}

// NewItemHandler creates an ItemHandler.
func NewItemHandler(media ItemMediaService, watch ItemWatchService, sessions ItemSessionCleaner, enricher ItemEnricher, matcher ItemMatchSearcher, webhooks ItemWebhookDispatcher, favorites ItemFavoriteChecker, tracker *streaming.Tracker, logger *slog.Logger) *ItemHandler {
	return &ItemHandler{media: media, watch: watch, sessions: sessions, enricher: enricher, matcher: matcher, webhooks: webhooks, favorites: favorites, tracker: tracker, logger: logger}
}

// WithMarkers attaches the intro/credits marker service. When nil, the Get
// response omits markers and the Markers endpoints are not served.
func (h *ItemHandler) WithMarkers(m ItemMarkerService) *ItemHandler {
	h.markers = m
	return h
}

// WithLibraryAccess attaches the library-access checker. When nil, all items
// pass — this matches the pre-ACL behavior. Wire it in to enforce per-library
// grants on item endpoints.
func (h *ItemHandler) WithLibraryAccess(a LibraryAccessChecker) *ItemHandler {
	h.access = a
	return h
}

// WithExternalSubtitles attaches the external subtitle lister so the Get
// response includes user-fetched subtitles (e.g. from OpenSubtitles)
// alongside the embedded streams.
func (h *ItemHandler) WithExternalSubtitles(s ExternalSubLister) *ItemHandler {
	h.subs = s
	return h
}

// WithSyncBroker attaches the cross-device sync broker. The Progress
// handler publishes a `progress.updated` event to the user's other
// connected SSE subscribers after a successful record so devices
// B/C/D refresh their resume position when device A reports new
// progress on the same item. When nil, sync is a no-op (the data
// still lands in the DB; devices just won't see it until they
// refetch the item).
func (h *ItemHandler) WithSyncBroker(b *notification.Broker) *ItemHandler {
	h.sync = b
	return h
}

// WithAudit attaches the audit logger. When nil, mutating operations skip
// audit emission (still functional, just unobserved).
func (h *ItemHandler) WithAudit(a *audit.Logger) *ItemHandler {
	h.audit = a
	return h
}

// checkLibraryAccess returns true if the caller is allowed to see items in the
// given library. Returns false + writes NotFound when denied. Returns false +
// writes InternalError on lookup failure. When the handler has no access
// checker wired (dev setups pre-migration), returns true to avoid lockouts.
func (h *ItemHandler) checkLibraryAccess(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
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

// AudioStreamJSON is the API representation of an audio stream.
type AudioStreamJSON struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Channels int    `json:"channels"`
	Language string `json:"language"`
	Title    string `json:"title"`
}

// SubtitleStreamJSON is the API representation of a subtitle stream.
type SubtitleStreamJSON struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Forced   bool   `json:"forced"`
}

// ItemFileResponse is the API representation of a media file.
type ItemFileResponse struct {
	ID                  string                 `json:"id"`
	StreamURL           string                 `json:"stream_url"`
	Container           *string                `json:"container,omitempty"`
	VideoCodec          *string                `json:"video_codec,omitempty"`
	AudioCodec          *string                `json:"audio_codec,omitempty"`
	ResolutionW         *int                   `json:"resolution_w,omitempty"`
	ResolutionH         *int                   `json:"resolution_h,omitempty"`
	Bitrate             *int64                 `json:"bitrate,omitempty"`
	HDRType             *string                `json:"hdr_type,omitempty"`
	DurationMS          *int64                 `json:"duration_ms,omitempty"`
	Faststart           bool                   `json:"faststart"`
	BitDepth            *int                   `json:"bit_depth,omitempty"`
	SampleRate          *int                   `json:"sample_rate,omitempty"`
	ChannelLayout       *string                `json:"channel_layout,omitempty"`
	Lossless            *bool                  `json:"lossless,omitempty"`
	ReplayGainTrackGain *float64               `json:"replaygain_track_gain,omitempty"`
	ReplayGainTrackPeak *float64               `json:"replaygain_track_peak,omitempty"`
	ReplayGainAlbumGain *float64               `json:"replaygain_album_gain,omitempty"`
	ReplayGainAlbumPeak *float64               `json:"replaygain_album_peak,omitempty"`
	AudioStreams        []AudioStreamJSON      `json:"audio_streams"`
	SubtitleStreams     []SubtitleStreamJSON   `json:"subtitle_streams"`
	ExternalSubtitles   []ExternalSubtitleJSON `json:"external_subtitles,omitempty"`
	Chapters            []ChapterJSON          `json:"chapters"`
}

// ChapterJSON is the API representation of a chapter marker.
type ChapterJSON struct {
	Title   string `json:"title"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// MarkerJSON is the API representation of an intro/credits marker.
// kind is one of: "intro", "credits". source is one of: "auto", "manual", "chapter".
type MarkerJSON struct {
	Kind    string `json:"kind"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Source  string `json:"source"`
}

// ItemDetailResponse is the full JSON response for a media item.
type ItemDetailResponse struct {
	ID            string             `json:"id"`
	LibraryID     string             `json:"library_id"`
	Title         string             `json:"title"`
	Type          string             `json:"type"`
	Year          *int               `json:"year,omitempty"`
	Summary       *string            `json:"summary,omitempty"`
	Rating        *float64           `json:"rating,omitempty"`
	DurationMS    *int64             `json:"duration_ms,omitempty"`
	PosterPath    *string            `json:"poster_path,omitempty"`
	FanartPath    *string            `json:"fanart_path,omitempty"`
	ContentRating *string            `json:"content_rating,omitempty"`
	Genres        []string           `json:"genres"`
	ParentID      *string            `json:"parent_id,omitempty"`
	Index         *int               `json:"index,omitempty"`
	ViewOffsetMS  int64              `json:"view_offset_ms"`
	// LastClientName carries the name of the device that last emitted a
	// scrobble/stop for this (user, media). Lets clients render "Resume
	// from Living Room TV" UX instead of a bare position. Nil = never
	// watched or client didn't identify itself.
	LastClientName *string `json:"last_client_name,omitempty"`
	IsFavorite    bool               `json:"is_favorite"`
	UpdatedAt     int64              `json:"updated_at"`
	Files         []ItemFileResponse `json:"files"`
	Markers       []MarkerJSON       `json:"markers,omitempty"`

	// Music-specific fields. Empty/nil for non-music items.
	MusicBrainzID             *string `json:"musicbrainz_id,omitempty"`
	MusicBrainzReleaseID      *string `json:"musicbrainz_release_id,omitempty"`
	MusicBrainzReleaseGroupID *string `json:"musicbrainz_release_group_id,omitempty"`
	MusicBrainzArtistID       *string `json:"musicbrainz_artist_id,omitempty"`
	MusicBrainzAlbumArtistID  *string `json:"musicbrainz_album_artist_id,omitempty"`
	DiscTotal                 *int    `json:"disc_total,omitempty"`
	TrackTotal                *int    `json:"track_total,omitempty"`
	OriginalYear              *int    `json:"original_year,omitempty"`
	Compilation               bool    `json:"compilation,omitempty"`
	ReleaseType               *string `json:"release_type,omitempty"`
}

// ChildItemResponse is the JSON representation of a child item (season/episode).
type ChildItemResponse struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Type         string    `json:"type"`
	Year         *int      `json:"year,omitempty"`
	Summary      *string   `json:"summary,omitempty"`
	Rating       *float64  `json:"rating,omitempty"`
	DurationMS   *int64    `json:"duration_ms,omitempty"`
	PosterPath   *string   `json:"poster_path,omitempty"`
	ThumbPath    *string   `json:"thumb_path,omitempty"`
	Index        *int      `json:"index,omitempty"`
	ViewOffsetMS int64     `json:"view_offset_ms"`
	Watched      bool      `json:"watched"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    int64     `json:"updated_at"`
}

// Get handles GET /api/v1/items/{id}.
func (h *ItemHandler) Get(w http.ResponseWriter, r *http.Request) {
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
		h.logger.ErrorContext(r.Context(), "get item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}

	// Enforce content rating restriction from claims.
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.Forbidden(w, r)
			return
		}
	}

	files, err := h.media.GetFiles(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get files for item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var viewOffsetMS int64
	var isFavorite bool
	var lastClientName *string
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		state, _ := h.watch.GetState(r.Context(), claims.UserID, id)
		if state.Status == "in_progress" {
			viewOffsetMS = state.PositionMS
		}
		lastClientName = state.LastClientName
		if h.favorites != nil {
			if fav, err := h.favorites.IsFavorite(r.Context(), claims.UserID, id); err == nil {
				isFavorite = fav
			}
		}
	}

	genres := item.Genres
	if genres == nil {
		genres = []string{}
	}

	out := ItemDetailResponse{
		ID:            item.ID.String(),
		LibraryID:     item.LibraryID.String(),
		Title:         item.Title,
		Type:          item.Type,
		Year:          item.Year,
		Summary:       item.Summary,
		Rating:        item.Rating,
		DurationMS:    item.DurationMS,
		PosterPath:    item.PosterPath,
		FanartPath:    item.FanartPath,
		ContentRating: item.ContentRating,
		Genres:        genres,
		Index:         item.Index,
		ViewOffsetMS:   viewOffsetMS,
		LastClientName: lastClientName,
		IsFavorite:     isFavorite,
		UpdatedAt:      item.UpdatedAt.UnixMilli(),
		Files:          make([]ItemFileResponse, 0, len(files)),
	}
	if item.ParentID != nil {
		s := item.ParentID.String()
		out.ParentID = &s
	}
	out.MusicBrainzID = uuidPtrToStringPtr(item.MusicBrainzID)
	out.MusicBrainzReleaseID = uuidPtrToStringPtr(item.MusicBrainzReleaseID)
	out.MusicBrainzReleaseGroupID = uuidPtrToStringPtr(item.MusicBrainzReleaseGroupID)
	out.MusicBrainzArtistID = uuidPtrToStringPtr(item.MusicBrainzArtistID)
	out.MusicBrainzAlbumArtistID = uuidPtrToStringPtr(item.MusicBrainzAlbumArtistID)
	out.DiscTotal = item.DiscTotal
	out.TrackTotal = item.TrackTotal
	out.OriginalYear = item.OriginalYear
	out.Compilation = item.Compilation
	out.ReleaseType = item.ReleaseType

	for _, f := range files {
		if f.Status != "active" {
			continue
		}
		fr := ItemFileResponse{
			ID:                  f.ID.String(),
			StreamURL:           "/media/stream/" + f.ID.String(),
			Container:           f.Container,
			VideoCodec:          f.VideoCodec,
			AudioCodec:          f.AudioCodec,
			ResolutionW:         f.ResolutionW,
			ResolutionH:         f.ResolutionH,
			Bitrate:             f.Bitrate,
			HDRType:             f.HDRType,
			DurationMS:          f.DurationMS,
			Faststart:           scanner.IsFaststart(f.FilePath),
			BitDepth:            f.BitDepth,
			SampleRate:          f.SampleRate,
			ChannelLayout:       f.ChannelLayout,
			Lossless:            f.Lossless,
			ReplayGainTrackGain: f.ReplayGainTrackGain,
			ReplayGainTrackPeak: f.ReplayGainTrackPeak,
			ReplayGainAlbumGain: f.ReplayGainAlbumGain,
			ReplayGainAlbumPeak: f.ReplayGainAlbumPeak,
			AudioStreams:        parseJSONBAudioStreams(f.AudioStreams),
			SubtitleStreams:     parseJSONBSubtitleStreams(f.SubtitleStreams),
			Chapters:            parseJSONBChapters(f.Chapters),
		}
		if h.subs != nil {
			if rows, err := h.subs.List(r.Context(), f.ID); err == nil && len(rows) > 0 {
				fr.ExternalSubtitles = make([]ExternalSubtitleJSON, 0, len(rows))
				for _, row := range rows {
					fr.ExternalSubtitles = append(fr.ExternalSubtitles, toExternalSubtitleJSON(row))
				}
			} else if err != nil {
				h.logger.WarnContext(r.Context(), "list external subs", "file_id", f.ID, "err", err)
			}
		}
		out.Files = append(out.Files, fr)
	}

	// Markers are only meaningful for episodes; movies are excluded by policy.
	if h.markers != nil && item.Type == "episode" {
		if ms, err := h.markers.List(r.Context(), id); err == nil {
			out.Markers = make([]MarkerJSON, 0, len(ms))
			for _, m := range ms {
				out.Markers = append(out.Markers, MarkerJSON{
					Kind:    m.Kind,
					StartMS: m.StartMS,
					EndMS:   m.EndMS,
					Source:  m.Source,
				})
			}
		} else {
			h.logger.WarnContext(r.Context(), "list markers", "item_id", id, "err", err)
		}
	}

	respond.Success(w, r, out)
}

// Children handles GET /api/v1/items/{id}/children.
func (h *ItemHandler) Children(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}

	parent, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for children", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, parent.LibraryID) {
		return
	}

	children, err := h.media.ListChildren(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list item children", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var userID uuid.UUID
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		userID = claims.UserID
	}

	out := make([]ChildItemResponse, len(children))
	for i, c := range children {
		var viewOffsetMS int64
		var watched bool
		if userID != uuid.Nil {
			state, _ := h.watch.GetState(r.Context(), userID, c.ID)
			switch state.Status {
			case "in_progress":
				viewOffsetMS = state.PositionMS
			case "watched":
				watched = true
			}
		}
		out[i] = ChildItemResponse{
			ID:           c.ID.String(),
			Title:        c.Title,
			Type:         c.Type,
			Year:         c.Year,
			Summary:      c.Summary,
			Rating:       c.Rating,
			DurationMS:   c.DurationMS,
			PosterPath:   c.PosterPath,
			ThumbPath:    c.ThumbPath,
			Index:        c.Index,
			ViewOffsetMS: viewOffsetMS,
			Watched:      watched,
			CreatedAt:    c.CreatedAt,
			UpdatedAt:    c.UpdatedAt.UnixMilli(),
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// PhotoEXIFResponse is the JSON shape returned by GET /items/{id}/exif. Every
// field is optional — missing EXIF tags simply remain nil.
type PhotoEXIFResponse struct {
	TakenAt       *time.Time `json:"taken_at,omitempty"`
	CameraMake    *string    `json:"camera_make,omitempty"`
	CameraModel   *string    `json:"camera_model,omitempty"`
	LensModel     *string    `json:"lens_model,omitempty"`
	FocalLengthMM *float64   `json:"focal_length_mm,omitempty"`
	Aperture      *float64   `json:"aperture,omitempty"`
	ShutterSpeed  *string    `json:"shutter_speed,omitempty"`
	ISO           *int32     `json:"iso,omitempty"`
	Flash         *bool      `json:"flash,omitempty"`
	Orientation   *int32     `json:"orientation,omitempty"`
	Width         *int32     `json:"width,omitempty"`
	Height        *int32     `json:"height,omitempty"`
	GPSLat        *float64   `json:"gps_lat,omitempty"`
	GPSLon        *float64   `json:"gps_lon,omitempty"`
	GPSAlt        *float64   `json:"gps_alt,omitempty"`
}

// GetEXIF handles GET /api/v1/items/{id}/exif. Returns 404 only when the
// item itself doesn't exist; an existing photo with no EXIF row (PNG with
// no EXIF block, scanner-skipped HEIC, etc.) returns 200 with empty fields
// — the photo is real, it just has no metadata, and the UI hides the
// missing rows via optional-chaining. Returning 404 here generated dev-
// console noise on every photo open.
func (h *ItemHandler) GetEXIF(w http.ResponseWriter, r *http.Request) {
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
		h.logger.ErrorContext(r.Context(), "get item for exif", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	pm, err := h.media.GetPhotoMetadata(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.Success(w, r, PhotoEXIFResponse{})
			return
		}
		h.logger.ErrorContext(r.Context(), "get photo metadata", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, PhotoEXIFResponse{
		TakenAt:       pm.TakenAt,
		CameraMake:    pm.CameraMake,
		CameraModel:   pm.CameraModel,
		LensModel:     pm.LensModel,
		FocalLengthMM: pm.FocalLengthMM,
		Aperture:      pm.Aperture,
		ShutterSpeed:  pm.ShutterSpeed,
		ISO:           pm.ISO,
		Flash:         pm.Flash,
		Orientation:   pm.Orientation,
		Width:         pm.Width,
		Height:        pm.Height,
		GPSLat:        pm.GPSLat,
		GPSLon:        pm.GPSLon,
		GPSAlt:        pm.GPSAlt,
	})
}

// ListMarkers handles GET /api/v1/items/{id}/markers. Returns an empty list
// for movies and containers so clients can call unconditionally.
func (h *ItemHandler) ListMarkers(w http.ResponseWriter, r *http.Request) {
	if h.markers == nil {
		respond.NotFound(w, r)
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
		h.logger.ErrorContext(r.Context(), "get item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	if item.Type != "episode" {
		respond.List(w, r, []MarkerJSON{}, 0, "")
		return
	}
	ms, err := h.markers.List(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list markers", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]MarkerJSON, 0, len(ms))
	for _, m := range ms {
		out = append(out, MarkerJSON{
			Kind:    m.Kind,
			StartMS: m.StartMS,
			EndMS:   m.EndMS,
			Source:  m.Source,
		})
	}
	respond.List(w, r, out, int64(len(out)), "")
}

// UpsertMarker handles PUT /api/v1/items/{id}/markers/{kind}. Admin only.
// Body: { "start_ms": int, "end_ms": int }. Overwrites auto markers.
func (h *ItemHandler) UpsertMarker(w http.ResponseWriter, r *http.Request) {
	if h.markers == nil {
		respond.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	kind := chi.URLParam(r, "kind")
	if kind != "intro" && kind != "credits" {
		respond.BadRequest(w, r, "kind must be intro or credits")
		return
	}
	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	if item.Type != "episode" {
		respond.BadRequest(w, r, "markers are only supported on episodes")
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	var body struct {
		StartMS int64 `json:"start_ms"`
		EndMS   int64 `json:"end_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	m, err := h.markers.Upsert(r.Context(), id, kind, body.StartMS, body.EndMS)
	if err != nil {
		respond.BadRequest(w, r, err.Error())
		return
	}
	respond.Success(w, r, MarkerJSON{
		Kind:    m.Kind,
		StartMS: m.StartMS,
		EndMS:   m.EndMS,
		Source:  m.Source,
	})
}

// DeleteMarker handles DELETE /api/v1/items/{id}/markers/{kind}. Admin only.
func (h *ItemHandler) DeleteMarker(w http.ResponseWriter, r *http.Request) {
	if h.markers == nil {
		respond.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	kind := chi.URLParam(r, "kind")
	if kind != "intro" && kind != "credits" {
		respond.BadRequest(w, r, "kind must be intro or credits")
		return
	}
	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	if err := h.markers.Delete(r.Context(), id, kind); err != nil {
		h.logger.ErrorContext(r.Context(), "delete marker", "id", id, "kind", kind, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// Progress handles PUT /api/v1/items/{id}/progress.
func (h *ItemHandler) Progress(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}

	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}

	if h.access != nil {
		item, err := h.media.GetItem(r.Context(), id)
		if err != nil {
			if errors.Is(err, media.ErrNotFound) {
				respond.NotFound(w, r)
				return
			}
			h.logger.ErrorContext(r.Context(), "get item for progress", "id", id, "err", err)
			respond.InternalError(w, r)
			return
		}
		if !h.checkLibraryAccess(w, r, item.LibraryID) {
			return
		}
	}

	var body struct {
		ViewOffsetMS int64  `json:"view_offset_ms"`
		DurationMS   int64  `json:"duration_ms"`
		State        string `json:"state"` // "playing"|"paused"|"stopped"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}

	eventType := itemStateToEventType(body.State)
	var durPtr *int64
	if body.DurationMS > 0 {
		durPtr = &body.DurationMS
	}

	if err := h.watch.Record(r.Context(), watchevent.RecordParams{
		UserID:     claims.UserID,
		MediaID:    id,
		EventType:  eventType,
		PositionMS: body.ViewOffsetMS,
		DurationMS: durPtr,
		OccurredAt: time.Now(),
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "record progress", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	// Dispatch webhook for discrete state changes (pause/stop).
	// "playing" is skipped — it fires on every position update and would flood endpoints.
	if h.webhooks != nil && (body.State == "paused" || body.State == "stopped") {
		h.webhooks.Dispatch(eventType, claims.UserID, id)
	}

	if h.tracker != nil {
		h.tracker.SetItemState(id, body.ViewOffsetMS, body.DurationMS)
	}

	if h.sessions != nil {
		if body.State == "stopped" {
			// Remove session immediately so it leaves "Now Playing" right away.
			if err := h.sessions.DeleteByMedia(r.Context(), id); err != nil {
				h.logger.WarnContext(r.Context(), "delete sessions on stop", "id", id, "err", err)
			}
		} else {
			// Keep position and last-activity timestamp fresh in Valkey.
			if err := h.sessions.UpdatePositionByMedia(r.Context(), id, body.ViewOffsetMS); err != nil {
				h.logger.WarnContext(r.Context(), "update session position", "id", id, "err", err)
			}
		}
	}

	// Cross-device sync. Publish the new position to the user's
	// other connected SSE subscribers so devices B/C/D refresh
	// their resume position. The publishing device gets the event
	// too — frontend filters out events whose origin matches its
	// own session via a tab-scoped origin token. Best-effort: the
	// progress data is already committed to the DB, so a missed
	// publish just means other devices catch up on their next
	// item refetch.
	if h.sync != nil {
		idStr := id.String()
		data, err := json.Marshal(struct {
			ItemID     string `json:"item_id"`
			PositionMS int64  `json:"position_ms"`
			DurationMS int64  `json:"duration_ms"`
			State      string `json:"state"`
		}{
			ItemID:     idStr,
			PositionMS: body.ViewOffsetMS,
			DurationMS: body.DurationMS,
			State:      body.State,
		})
		if err == nil {
			h.sync.Publish(claims.UserID, notification.Event{
				Type:      "progress.updated",
				ItemID:    &idStr,
				CreatedAt: time.Now().UnixMilli(),
				Data:      data,
			})
		}
	}

	respond.NoContent(w)
}

// Enrich handles POST /api/v1/items/{id}/enrich.
// Re-runs the metadata enrichment pipeline for a single item on demand —
// useful when a scan ran without a TMDB key configured, or when artwork
// is missing due to a transient download failure.
func (h *ItemHandler) Enrich(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.enricher == nil {
		respond.BadRequest(w, r, "metadata enrichment not configured")
		return
	}
	// Verify the item exists before kicking off background work so we can
	// return an accurate 404 and avoid spamming TMDB on guessed IDs.
	if _, err := h.media.GetItem(r.Context(), id); err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for enrich", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		actor := claims.UserID
		h.audit.Log(r.Context(), &actor, audit.ActionItemEnrich, id.String(), nil, audit.ClientIP(r))
	}
	// Run enrichment in the background so the request returns immediately.
	// Use WithoutCancel so the work continues after the HTTP request ends
	// but still inherits tracing/observability from the original context.
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		if err := h.enricher.EnrichItem(bgCtx, id); err != nil {
			h.logger.WarnContext(bgCtx, "on-demand enrich failed", "id", id, "err", err)
		}
	}()
	respond.NoContent(w)
}

// SearchMatch handles GET /api/v1/items/{id}/match/search?query=...
// Returns TMDB candidates so the user can pick the correct match.
// Admin-only: pairs with ApplyMatch as part of the manual-match workflow,
// and the per-query TMDB call costs against the operator's API quota.
func (h *ItemHandler) SearchMatch(w http.ResponseWriter, r *http.Request) {
	if claims := middleware.ClaimsFromContext(r.Context()); claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.matcher == nil {
		respond.BadRequest(w, r, "metadata matching not configured")
		return
	}

	query := r.URL.Query().Get("query")
	if query == "" {
		respond.BadRequest(w, r, "query parameter required")
		return
	}

	// Determine item type to search the right TMDB endpoint.
	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for match search", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var candidates []MatchCandidate
	switch item.Type {
	case "show":
		candidates, err = h.matcher.SearchTVCandidates(r.Context(), query)
	case "movie":
		candidates, err = h.matcher.SearchMovieCandidates(r.Context(), query)
	default:
		respond.BadRequest(w, r, "match search only supports show and movie items")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "match search", "id", id, "query", query, "err", err)
		respond.InternalError(w, r)
		return
	}

	respond.Success(w, r, candidates)
}

// ApplyMatch handles POST /api/v1/items/{id}/match.
// Sets the TMDB ID for an item and re-enriches it from that specific match.
// Admin-only: this rewrites globally-visible metadata for the item.
func (h *ItemHandler) ApplyMatch(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.enricher == nil {
		respond.BadRequest(w, r, "metadata enrichment not configured")
		return
	}
	if _, err := h.media.GetItem(r.Context(), id); err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for match", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var body struct {
		TMDBID int `json:"tmdb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TMDBID <= 0 {
		respond.BadRequest(w, r, "tmdb_id is required and must be positive")
		return
	}

	if h.audit != nil {
		actor := claims.UserID
		h.audit.Log(r.Context(), &actor, audit.ActionItemMatchApply, id.String(),
			map[string]any{"tmdb_id": body.TMDBID}, audit.ClientIP(r))
	}

	// Run in background so the request returns immediately.
	bgCtx := context.WithoutCancel(r.Context())
	go func() {
		if err := h.enricher.MatchItem(bgCtx, id, body.TMDBID); err != nil {
			h.logger.WarnContext(bgCtx, "apply match failed", "id", id, "tmdb_id", body.TMDBID, "err", err)
		}
	}()
	respond.NoContent(w)
}

// StreamFile handles GET /media/stream/{id}.
// Looks up the file in the DB and serves it directly using the stored absolute
// path. Requires authentication — browser <video> elements send same-origin
// cookies, so the cookie auth path in the auth middleware applies here too.
// Enforces per-library ACL and content-rating restriction on the parent item.
func (h *ItemHandler) StreamFile(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid file id")
		return
	}

	file, err := h.media.GetFile(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get file for stream", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	if file.Status != "active" {
		respond.NotFound(w, r)
		return
	}

	// Enforce per-library ACL and content rating restriction via the parent item.
	item, err := h.media.GetItem(r.Context(), file.MediaItemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for stream", "file_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil && claims.MaxContentRating != "" {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.Forbidden(w, r)
			return
		}
	}

	if h.tracker != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		clientIP := r.RemoteAddr
		if host, _, err := net.SplitHostPort(clientIP); err == nil {
			clientIP = host
		}
		clientName := r.Header.Get("User-Agent")
		if idx := strings.IndexByte(clientName, '/'); idx > 0 {
			clientName = clientName[:idx]
		}
		h.tracker.Touch(clientIP, file.FilePath, clientName)
	}

	http.ServeFile(w, r, file.FilePath)
}

func itemStateToEventType(state string) string {
	switch state {
	case "paused":
		return "pause"
	case "stopped":
		return "stop"
	default:
		return "play"
	}
}

func parseJSONBAudioStreams(data []byte) []AudioStreamJSON {
	if len(data) == 0 {
		return []AudioStreamJSON{}
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return []AudioStreamJSON{}
	}
	out := make([]AudioStreamJSON, 0, len(raw))
	for _, s := range raw {
		out = append(out, AudioStreamJSON{
			Index:    int(asFloat64(s["index"])),
			Codec:    asString(s["codec"]),
			Channels: int(asFloat64(s["channels"])),
			Language: asString(s["language"]),
			Title:    asString(s["title"]),
		})
	}
	return out
}

func parseJSONBChapters(data []byte) []ChapterJSON {
	if len(data) == 0 {
		return []ChapterJSON{}
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return []ChapterJSON{}
	}
	out := make([]ChapterJSON, 0, len(raw))
	for _, c := range raw {
		out = append(out, ChapterJSON{
			Title:   asString(c["title"]),
			StartMS: int64(asFloat64(c["start_ms"])),
			EndMS:   int64(asFloat64(c["end_ms"])),
		})
	}
	return out
}

func parseJSONBSubtitleStreams(data []byte) []SubtitleStreamJSON {
	if len(data) == 0 {
		return []SubtitleStreamJSON{}
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return []SubtitleStreamJSON{}
	}
	out := make([]SubtitleStreamJSON, 0, len(raw))
	for _, s := range raw {
		out = append(out, SubtitleStreamJSON{
			Index:    int(asFloat64(s["index"])),
			Codec:    asString(s["codec"]),
			Language: asString(s["language"]),
			Title:    asString(s["title"]),
			Forced:   asBool(s["forced"]),
		})
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asFloat64(v any) float64 {
	f, _ := v.(float64)
	return f
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func uuidPtrToStringPtr(u *uuid.UUID) *string {
	if u == nil {
		return nil
	}
	s := u.String()
	return &s
}

// ServeSubtitle handles GET /media/subtitles/{fileId}/{streamIndex}.
// Extracts a subtitle stream from the media file and returns it as WebVTT.
// Only text-based subtitle codecs (srt, ass, subrip, mov_text, webvtt) are supported.
func (h *ItemHandler) ServeSubtitle(w http.ResponseWriter, r *http.Request) {
	fileID, err := uuid.Parse(chi.URLParam(r, "fileId"))
	if err != nil {
		respond.BadRequest(w, r, "invalid file id")
		return
	}
	streamIdx, err := strconv.Atoi(chi.URLParam(r, "streamIndex"))
	if err != nil || streamIdx < 0 {
		respond.BadRequest(w, r, "invalid stream index")
		return
	}

	file, err := h.media.GetFile(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get file for subtitle", "id", fileID, "err", err)
		respond.InternalError(w, r)
		return
	}

	if file.Status != "active" {
		respond.NotFound(w, r)
		return
	}

	// Enforce per-library ACL and content rating via the parent item.
	item, err := h.media.GetItem(r.Context(), file.MediaItemID)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return
		}
		h.logger.ErrorContext(r.Context(), "get item for subtitle", "file_id", fileID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if !h.checkLibraryAccess(w, r, item.LibraryID) {
		return
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil && claims.MaxContentRating != "" {
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.Forbidden(w, r)
			return
		}
	}

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")

	cmd := exec.CommandContext(r.Context(), "ffmpeg",
		"-i", file.FilePath,
		"-map", fmt.Sprintf("0:%d", streamIdx),
		"-f", "webvtt",
		"-v", "quiet",
		"pipe:1",
	)
	cmd.Stdout = w
	if err := cmd.Run(); err != nil {
		// If we haven't written headers yet, return an error.
		// Otherwise the connection was likely closed by the client.
		h.logger.WarnContext(r.Context(), "subtitle extraction failed",
			"file_id", fileID, "stream", streamIdx, "err", err)
	}
}
