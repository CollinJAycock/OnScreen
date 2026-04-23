package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/lyrics"
)

// LyricsStore is the slice of sqlc the lyrics handler needs. Kept narrow
// so a fake stands in for it in tests.
type LyricsStore interface {
	GetLyrics(ctx context.Context, itemID uuid.UUID) (plain string, synced string, err error)
	SetLyrics(ctx context.Context, itemID uuid.UUID, plain, synced string) error
}

// LyricsItemSource returns enough metadata about a track for LRCLIB
// lookup: artist name, track title, album name, duration seconds. The
// media service already has these; the handler just needs the right
// slice.
type LyricsItemSource interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
	GetFiles(ctx context.Context, itemID uuid.UUID) ([]media.File, error)
	// GetTrackMetadata is defined so the adapter can resolve artist +
	// album names from the track's parent chain (track → album → artist)
	// in one round-trip instead of two GetItem calls.
	GetTrackMetadata(ctx context.Context, trackID uuid.UUID) (artist, album string, err error)
}

// LyricsHandler serves GET /api/v1/items/{id}/lyrics.
type LyricsHandler struct {
	store   LyricsStore
	items   LyricsItemSource
	fetcher lyrics.Fetcher
	logger  *slog.Logger
	access  LibraryAccessChecker
}

// NewLyricsHandler wires the handler. fetcher may be nil; when absent,
// the handler still serves tag-extracted lyrics but skips the external
// lookup fallback.
func NewLyricsHandler(store LyricsStore, items LyricsItemSource, fetcher lyrics.Fetcher, logger *slog.Logger) *LyricsHandler {
	return &LyricsHandler{store: store, items: items, fetcher: fetcher, logger: logger}
}

// WithLibraryAccess attaches the per-library ACL checker.
func (h *LyricsHandler) WithLibraryAccess(a LibraryAccessChecker) *LyricsHandler {
	h.access = a
	return h
}

// LyricsResponse is the JSON shape. Plain and Synced may each be
// empty — clients prefer synced when present and fall back to plain.
type LyricsResponse struct {
	Plain  string `json:"plain"`
	Synced string `json:"synced"`
}

// Get handles the request. Flow:
//  1. Load the cached lyrics row.
//  2. If either field is non-empty, return immediately.
//  3. Otherwise, resolve the track's artist/album/duration and hit
//     LRCLIB. On success, persist and return.
//  4. On LRCLIB miss, return an empty response (not 404) — the track
//     might be instrumental or simply not indexed, and clients render
//     "No lyrics available" rather than a hard error.
func (h *LyricsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return
	}
	item, err := h.items.GetItem(r.Context(), id)
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
	if item.Type != "track" {
		// Lyrics apply only to tracks; for other types it's just noise.
		respond.NotFound(w, r)
		return
	}

	plain, synced, err := h.store.GetLyrics(r.Context(), id)
	if err == nil && (plain != "" || synced != "") {
		respond.Success(w, r, LyricsResponse{Plain: plain, Synced: synced})
		return
	}

	// LRCLIB fallback. If the fetcher isn't configured (tests, or
	// server-side disabled lookup), return empty rather than 404 so
	// clients keep rendering the "no lyrics" placeholder.
	if h.fetcher == nil {
		respond.Success(w, r, LyricsResponse{})
		return
	}

	// Duration in seconds — item.DurationMS is milliseconds.
	var durationS int
	if item.DurationMS != nil {
		durationS = int(*item.DurationMS / 1000)
	}

	artist, album, err := h.items.GetTrackMetadata(r.Context(), id)
	if err != nil {
		// Metadata resolution shouldn't 500 the endpoint — degrade.
		h.logger.WarnContext(r.Context(), "lyrics: track metadata lookup",
			"item_id", id, "err", err)
		respond.Success(w, r, LyricsResponse{})
		return
	}

	res, err := h.fetcher.Lookup(r.Context(), lyrics.LookupParams{
		Artist:    artist,
		Track:     item.Title,
		Album:     album,
		DurationS: durationS,
	})
	if errors.Is(err, lyrics.ErrNotFound) {
		// Cache the miss as empty strings so we don't keep re-hitting
		// LRCLIB for instrumental/unknown tracks.
		_ = h.store.SetLyrics(r.Context(), id, "", "")
		respond.Success(w, r, LyricsResponse{})
		return
	}
	if err != nil {
		h.logger.WarnContext(r.Context(), "lyrics fetch",
			"item_id", id, "err", err)
		respond.Success(w, r, LyricsResponse{})
		return
	}
	if err := h.store.SetLyrics(r.Context(), id, res.Plain, res.Synced); err != nil {
		h.logger.WarnContext(r.Context(), "lyrics persist",
			"item_id", id, "err", err)
	}
	respond.Success(w, r, LyricsResponse{Plain: res.Plain, Synced: res.Synced})
}

func (h *LyricsHandler) checkLibraryAccess(w http.ResponseWriter, r *http.Request, libraryID uuid.UUID) bool {
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
		h.logger.ErrorContext(r.Context(), "lyrics: library access check",
			"library_id", libraryID, "err", err)
		respond.InternalError(w, r)
		return false
	}
	if !ok {
		respond.NotFound(w, r)
		return false
	}
	return true
}
