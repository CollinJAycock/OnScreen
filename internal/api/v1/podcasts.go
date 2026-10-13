package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/podcast"
)

// PodcastService is the podcast subscription surface the handler drives.
// Satisfied by *podcast.Service.
type PodcastService interface {
	Subscribe(ctx context.Context, p podcast.SubscribeParams) (gen.PodcastFeed, error)
	Feeds(ctx context.Context, libraryID *uuid.UUID) ([]gen.ListPodcastFeedsRow, error)
	Feed(ctx context.Context, feedID uuid.UUID) (gen.PodcastFeed, error)
	Episodes(ctx context.Context, feedID uuid.UUID, limit, offset int32) ([]gen.PodcastFeedEpisode, int64, error)
	UpdateSettings(ctx context.Context, feedID uuid.UUID, autoDownload bool, keepLatest int) (gen.PodcastFeed, error)
	Refresh(ctx context.Context, feedID uuid.UUID) (podcast.RefreshResult, error)
	Unsubscribe(ctx context.Context, feedID uuid.UUID, deleteFiles bool) error
	Download(ctx context.Context, episodeID uuid.UUID) (gen.PodcastFeedEpisode, error)
	DeleteDownload(ctx context.Context, episodeID uuid.UUID) error
}

// PodcastHandler serves the admin podcast subscription API: subscribe a
// podcast library to an RSS feed, list and download its episodes, set
// auto-download and retention, unsubscribe.
type PodcastHandler struct {
	svc    PodcastService
	logger *slog.Logger
	audit  *audit.Logger
}

// NewPodcastHandler builds the handler.
func NewPodcastHandler(svc PodcastService, logger *slog.Logger) *PodcastHandler {
	return &PodcastHandler{svc: svc, logger: logger}
}

// WithAudit attaches an audit logger.
func (h *PodcastHandler) WithAudit(a *audit.Logger) *PodcastHandler {
	h.audit = a
	return h
}

// ── DTOs ──────────────────────────────────────────────────────────────────

type podcastFeedDTO struct {
	ID              uuid.UUID  `json:"id"`
	LibraryID       uuid.UUID  `json:"library_id"`
	FeedURL         string     `json:"feed_url"`
	Title           string     `json:"title"`
	Author          *string    `json:"author,omitempty"`
	Description     *string    `json:"description,omitempty"`
	ImageURL        *string    `json:"image_url,omitempty"`
	SiteURL         *string    `json:"site_url,omitempty"`
	Language        *string    `json:"language,omitempty"`
	Folder          string     `json:"folder"`
	AutoDownload    bool       `json:"auto_download"`
	KeepLatest      int32      `json:"keep_latest"`
	LastCheckedAt   *string    `json:"last_checked_at,omitempty"`
	LastError       *string    `json:"last_error,omitempty"`
	ShowItemID      *uuid.UUID `json:"show_item_id,omitempty"`
	EpisodeCount    *int64     `json:"episode_count,omitempty"`
	DownloadedCount *int64     `json:"downloaded_count,omitempty"`
	CreatedAt       string     `json:"created_at"`
}

func toPodcastFeedDTO(f gen.PodcastFeed) podcastFeedDTO {
	return podcastFeedDTO{
		ID:            f.ID,
		LibraryID:     f.LibraryID,
		FeedURL:       f.FeedUrl,
		Title:         f.Title,
		Author:        f.Author,
		Description:   f.Description,
		ImageURL:      f.ImageUrl,
		SiteURL:       f.SiteUrl,
		Language:      f.Language,
		Folder:        f.Folder,
		AutoDownload:  f.AutoDownload,
		KeepLatest:    f.KeepLatest,
		LastCheckedAt: rfc3339(f.LastCheckedAt),
		LastError:     f.LastError,
		ShowItemID:    pgUUIDPtr(f.ShowItemID),
		CreatedAt:     f.CreatedAt.Time.UTC().Format(time.RFC3339),
	}
}

func toPodcastFeedListDTO(r gen.ListPodcastFeedsRow) podcastFeedDTO {
	d := toPodcastFeedDTO(gen.PodcastFeed{
		ID: r.ID, LibraryID: r.LibraryID, FeedUrl: r.FeedUrl, Title: r.Title,
		Author: r.Author, Description: r.Description, ImageUrl: r.ImageUrl,
		SiteUrl: r.SiteUrl, Language: r.Language, Folder: r.Folder,
		AutoDownload: r.AutoDownload, KeepLatest: r.KeepLatest,
		LastCheckedAt: r.LastCheckedAt, LastError: r.LastError,
		ShowItemID: r.ShowItemID, CreatedAt: r.CreatedAt,
	})
	episodes, downloaded := r.EpisodeCount, r.DownloadedCount
	d.EpisodeCount, d.DownloadedCount = &episodes, &downloaded
	return d
}

type podcastEpisodeDTO struct {
	ID              uuid.UUID  `json:"id"`
	FeedID          uuid.UUID  `json:"feed_id"`
	Title           string     `json:"title"`
	Description     *string    `json:"description,omitempty"`
	PublishedAt     *string    `json:"published_at,omitempty"`
	DurationMS      *int64     `json:"duration_ms,omitempty"`
	EnclosureLength *int64     `json:"enclosure_length,omitempty"`
	Season          *int32     `json:"season,omitempty"`
	Episode         *int32     `json:"episode,omitempty"`
	Status          string     `json:"status"`
	Error           *string    `json:"error,omitempty"`
	MediaItemID     *uuid.UUID `json:"media_item_id,omitempty"`
	DownloadedAt    *string    `json:"downloaded_at,omitempty"`
}

func toPodcastEpisodeDTO(e gen.PodcastFeedEpisode) podcastEpisodeDTO {
	return podcastEpisodeDTO{
		ID:              e.ID,
		FeedID:          e.FeedID,
		Title:           e.Title,
		Description:     e.Description,
		PublishedAt:     rfc3339(e.PublishedAt),
		DurationMS:      e.DurationMs,
		EnclosureLength: e.EnclosureLength,
		Season:          e.Season,
		Episode:         e.Episode,
		Status:          e.Status,
		Error:           e.Error,
		MediaItemID:     pgUUIDPtr(e.MediaItemID),
		DownloadedAt:    rfc3339(e.DownloadedAt),
	}
}

func rfc3339(t pgtype.Timestamptz) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.UTC().Format(time.RFC3339)
	return &s
}

func pgUUIDPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

// ── Handlers ──────────────────────────────────────────────────────────────

// List handles GET /api/v1/admin/podcast-feeds[?library_id=].
func (h *PodcastHandler) List(w http.ResponseWriter, r *http.Request) {
	var libraryID *uuid.UUID
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			respond.BadRequest(w, r, "invalid library_id")
			return
		}
		libraryID = &id
	}
	rows, err := h.svc.Feeds(r.Context(), libraryID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list podcast feeds", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]podcastFeedDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPodcastFeedListDTO(row))
	}
	respond.List(w, r, out, int64(len(out)), "")
}

type podcastSubscribeBody struct {
	LibraryID      uuid.UUID `json:"library_id"`
	FeedURL        string    `json:"feed_url"`
	AutoDownload   *bool     `json:"auto_download"`
	KeepLatest     *int      `json:"keep_latest"`
	DownloadLatest *int      `json:"download_latest"`
}

// Create handles POST /api/v1/admin/podcast-feeds: subscribe a podcast
// library to a feed. Defaults: auto-download on, keep everything, download
// the newest episode now.
func (h *PodcastHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body podcastSubscribeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if body.LibraryID == uuid.Nil {
		respond.ValidationError(w, r, "library_id is required")
		return
	}
	if body.FeedURL == "" {
		respond.ValidationError(w, r, "feed_url is required")
		return
	}
	p := podcast.SubscribeParams{
		LibraryID:      body.LibraryID,
		FeedURL:        body.FeedURL,
		AutoDownload:   true,
		DownloadLatest: 1,
	}
	if body.AutoDownload != nil {
		p.AutoDownload = *body.AutoDownload
	}
	if body.KeepLatest != nil {
		p.KeepLatest = *body.KeepLatest
	}
	if body.DownloadLatest != nil {
		p.DownloadLatest = *body.DownloadLatest
	}
	if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
		uid := claims.UserID
		p.CreatedBy = &uid
	}
	feed, err := h.svc.Subscribe(r.Context(), p)
	if err != nil {
		h.writeError(w, r, "subscribe podcast", err)
		return
	}
	h.auditEvent(r, audit.ActionPodcastSubscribe, feed.ID.String(), map[string]any{
		"library_id":      feed.LibraryID,
		"feed_url":        feed.FeedUrl,
		"title":           feed.Title,
		"auto_download":   feed.AutoDownload,
		"keep_latest":     feed.KeepLatest,
		"download_latest": p.DownloadLatest,
	})
	respond.Created(w, r, toPodcastFeedDTO(feed))
}

// Get handles GET /api/v1/admin/podcast-feeds/{id}.
func (h *PodcastHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid feed id")
		return
	}
	feed, err := h.svc.Feed(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get podcast feed", err)
		return
	}
	respond.Success(w, r, toPodcastFeedDTO(feed))
}

type podcastUpdateBody struct {
	AutoDownload *bool `json:"auto_download"`
	KeepLatest   *int  `json:"keep_latest"`
}

// Update handles PATCH /api/v1/admin/podcast-feeds/{id}: auto-download and
// retention. A lower keep_latest deletes the extra downloads at once.
func (h *PodcastHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid feed id")
		return
	}
	var body podcastUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	cur, err := h.svc.Feed(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get podcast feed", err)
		return
	}
	autoDownload, keepLatest := cur.AutoDownload, int(cur.KeepLatest)
	if body.AutoDownload != nil {
		autoDownload = *body.AutoDownload
	}
	if body.KeepLatest != nil {
		keepLatest = *body.KeepLatest
	}
	feed, err := h.svc.UpdateSettings(r.Context(), id, autoDownload, keepLatest)
	if err != nil {
		h.writeError(w, r, "update podcast feed", err)
		return
	}
	h.auditEvent(r, audit.ActionPodcastUpdate, feed.ID.String(), map[string]any{
		"title":         feed.Title,
		"auto_download": feed.AutoDownload,
		"keep_latest":   feed.KeepLatest,
	})
	respond.Success(w, r, toPodcastFeedDTO(feed))
}

// Delete handles DELETE /api/v1/admin/podcast-feeds/{id}[?delete_files=true].
// Without delete_files the downloaded episodes stay in the library.
func (h *PodcastHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid feed id")
		return
	}
	deleteFiles := r.URL.Query().Get("delete_files") == "true"
	feed, err := h.svc.Feed(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "get podcast feed", err)
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), id, deleteFiles); err != nil {
		h.writeError(w, r, "unsubscribe podcast", err)
		return
	}
	h.auditEvent(r, audit.ActionPodcastUnsubscribe, id.String(), map[string]any{
		"title":         feed.Title,
		"feed_url":      feed.FeedUrl,
		"files_deleted": deleteFiles,
	})
	respond.NoContent(w)
}

// Refresh handles POST /api/v1/admin/podcast-feeds/{id}/refresh: re-read the
// feed now instead of at the next hourly refresh.
func (h *PodcastHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid feed id")
		return
	}
	res, err := h.svc.Refresh(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "refresh podcast feed", err)
		return
	}
	respond.Success(w, r, res)
}

// Episodes handles GET /api/v1/admin/podcast-feeds/{id}/episodes, newest
// first, paged by limit/offset.
func (h *PodcastHandler) Episodes(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid feed id")
		return
	}
	page := respond.ParsePagination(r, 50, 200)
	rows, total, err := h.svc.Episodes(r.Context(), id, page.Limit, page.Offset)
	if err != nil {
		h.writeError(w, r, "list podcast episodes", err)
		return
	}
	out := make([]podcastEpisodeDTO, 0, len(rows))
	for _, e := range rows {
		out = append(out, toPodcastEpisodeDTO(e))
	}
	respond.List(w, r, out, total, "")
}

// DownloadEpisode handles POST /api/v1/admin/podcast-episodes/{id}/download.
// The download runs in the background; the episode comes back as
// "downloading" and the feed's episode list shows when it's done.
func (h *PodcastHandler) DownloadEpisode(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid episode id")
		return
	}
	ep, err := h.svc.Download(r.Context(), id)
	if err != nil {
		h.writeError(w, r, "download podcast episode", err)
		return
	}
	respond.Accepted(w, r, toPodcastEpisodeDTO(ep))
}

// DeleteEpisodeDownload handles DELETE
// /api/v1/admin/podcast-episodes/{id}/download: delete the downloaded file.
// The episode stays listed, and auto-download won't fetch it again.
func (h *PodcastHandler) DeleteEpisodeDownload(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid episode id")
		return
	}
	if err := h.svc.DeleteDownload(r.Context(), id); err != nil {
		h.writeError(w, r, "delete podcast download", err)
		return
	}
	respond.NoContent(w)
}

// writeError maps service errors to responses; anything unexpected is a 500.
func (h *PodcastHandler) writeError(w http.ResponseWriter, r *http.Request, op string, err error) {
	var fetchErr *podcast.FetchError
	switch {
	case errors.Is(err, podcast.ErrFeedNotFound), errors.Is(err, podcast.ErrEpisodeNotFound),
		errors.Is(err, library.ErrNotFound):
		respond.NotFound(w, r)
	case errors.Is(err, podcast.ErrAlreadySubscribed):
		respond.Error(w, r, http.StatusConflict, "ALREADY_SUBSCRIBED", err.Error())
	case errors.Is(err, podcast.ErrNotDownloadable), errors.Is(err, podcast.ErrNotDownloaded):
		respond.Error(w, r, http.StatusConflict, "EPISODE_STATE", err.Error())
	case errors.Is(err, podcast.ErrNotPodcastLibrary), errors.Is(err, podcast.ErrNoLibraryPath),
		errors.Is(err, podcast.ErrInvalidFeedURL), errors.Is(err, podcast.ErrNoAudioEpisodes),
		errors.Is(err, podcast.ErrInvalidKeepLatest), errors.Is(err, podcast.ErrInvalidDownloadSize):
		respond.ValidationError(w, r, err.Error())
	case errors.As(err, &fetchErr):
		respond.Error(w, r, http.StatusBadGateway, "FEED_UNAVAILABLE", fetchErr.Msg)
	default:
		h.logger.ErrorContext(r.Context(), op, "err", err)
		respond.InternalError(w, r)
	}
}

func (h *PodcastHandler) auditEvent(r *http.Request, action, target string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		return
	}
	actor := claims.UserID
	h.audit.Log(r.Context(), &actor, action, target, detail, audit.ClientIP(r))
}
