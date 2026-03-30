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
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/domain/watchevent"
	"github.com/onscreen/onscreen/internal/streaming"
)

// ItemMediaService defines the media domain operations the items handler needs.
type ItemMediaService interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFile(ctx context.Context, id uuid.UUID) (*media.File, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	ListChildren(ctx context.Context, parentID uuid.UUID) ([]media.Item, error)
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

// ItemHandler handles /api/v1/items.
type ItemHandler struct {
	media    ItemMediaService
	watch    ItemWatchService
	sessions ItemSessionCleaner
	enricher ItemEnricher
	matcher  ItemMatchSearcher
	webhooks ItemWebhookDispatcher
	tracker  *streaming.Tracker
	logger   *slog.Logger
}

// NewItemHandler creates an ItemHandler.
func NewItemHandler(media ItemMediaService, watch ItemWatchService, sessions ItemSessionCleaner, enricher ItemEnricher, matcher ItemMatchSearcher, webhooks ItemWebhookDispatcher, tracker *streaming.Tracker, logger *slog.Logger) *ItemHandler {
	return &ItemHandler{media: media, watch: watch, sessions: sessions, enricher: enricher, matcher: matcher, webhooks: webhooks, tracker: tracker, logger: logger}
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
	ID              string               `json:"id"`
	StreamURL       string               `json:"stream_url"`
	Container       *string              `json:"container,omitempty"`
	VideoCodec      *string              `json:"video_codec,omitempty"`
	AudioCodec      *string              `json:"audio_codec,omitempty"`
	ResolutionW     *int                 `json:"resolution_w,omitempty"`
	ResolutionH     *int                 `json:"resolution_h,omitempty"`
	Bitrate         *int64               `json:"bitrate,omitempty"`
	HDRType         *string              `json:"hdr_type,omitempty"`
	DurationMS      *int64               `json:"duration_ms,omitempty"`
	AudioStreams    []AudioStreamJSON    `json:"audio_streams"`
	SubtitleStreams []SubtitleStreamJSON `json:"subtitle_streams"`
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
	UpdatedAt     int64              `json:"updated_at"`
	Files         []ItemFileResponse `json:"files"`
}

// ChildItemResponse is the JSON representation of a child item (season/episode).
type ChildItemResponse struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Type       string    `json:"type"`
	Year       *int      `json:"year,omitempty"`
	Summary    *string   `json:"summary,omitempty"`
	Rating     *float64  `json:"rating,omitempty"`
	DurationMS *int64    `json:"duration_ms,omitempty"`
	PosterPath *string   `json:"poster_path,omitempty"`
	ThumbPath  *string   `json:"thumb_path,omitempty"`
	Index      *int      `json:"index,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  int64     `json:"updated_at"`
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

	files, err := h.media.GetFiles(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get files for item", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	var viewOffsetMS int64
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		state, _ := h.watch.GetState(r.Context(), claims.UserID, id)
		if state.Status == "in_progress" {
			viewOffsetMS = state.PositionMS
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
		ViewOffsetMS:  viewOffsetMS,
		UpdatedAt:     item.UpdatedAt.UnixMilli(),
		Files:         make([]ItemFileResponse, 0, len(files)),
	}
	if item.ParentID != nil {
		s := item.ParentID.String()
		out.ParentID = &s
	}

	for _, f := range files {
		if f.Status != "active" {
			continue
		}
		out.Files = append(out.Files, ItemFileResponse{
			ID:              f.ID.String(),
			StreamURL:       "/media/stream/" + f.ID.String(),
			Container:       f.Container,
			VideoCodec:      f.VideoCodec,
			AudioCodec:      f.AudioCodec,
			ResolutionW:     f.ResolutionW,
			ResolutionH:     f.ResolutionH,
			Bitrate:         f.Bitrate,
			HDRType:         f.HDRType,
			DurationMS:      f.DurationMS,
			AudioStreams:    parseJSONBAudioStreams(f.AudioStreams),
			SubtitleStreams: parseJSONBSubtitleStreams(f.SubtitleStreams),
		})
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

	children, err := h.media.ListChildren(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list item children", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}

	out := make([]ChildItemResponse, len(children))
	for i, c := range children {
		out[i] = ChildItemResponse{
			ID:         c.ID.String(),
			Title:      c.Title,
			Type:       c.Type,
			Year:       c.Year,
			Summary:    c.Summary,
			Rating:     c.Rating,
			DurationMS: c.DurationMS,
			PosterPath: c.PosterPath,
			ThumbPath:  c.ThumbPath,
			Index:      c.Index,
			CreatedAt:  c.CreatedAt,
			UpdatedAt:  c.UpdatedAt.UnixMilli(),
		}
	}
	respond.List(w, r, out, int64(len(out)), "")
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

	respond.NoContent(w)
}

// Enrich handles POST /api/v1/items/{id}/enrich.
// Re-runs the metadata enrichment pipeline for a single item on demand —
// useful when a scan ran without a TMDB key configured, or when artwork
// is missing due to a transient download failure.
func (h *ItemHandler) Enrich(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.enricher == nil {
		respond.BadRequest(w, r, "metadata enrichment not configured")
		return
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
func (h *ItemHandler) SearchMatch(w http.ResponseWriter, r *http.Request) {
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
func (h *ItemHandler) ApplyMatch(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	if h.enricher == nil {
		respond.BadRequest(w, r, "metadata enrichment not configured")
		return
	}

	var body struct {
		TMDBID int `json:"tmdb_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TMDBID <= 0 {
		respond.BadRequest(w, r, "tmdb_id is required and must be positive")
		return
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
// path, bypassing any filepath.Rel computation. No auth required — the UUID is
// opaque and the browser video element cannot send auth headers.
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

	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Access-Control-Allow-Origin", "*")

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
