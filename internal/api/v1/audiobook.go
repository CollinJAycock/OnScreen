package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// Listening speed and bookmarks, per user, for audiobooks. Both hang off the
// book: a chapter's speed is its book's, and a book lists every bookmark in
// it, whichever chapter each sits in.

const (
	// MinPlaybackRate / MaxPlaybackRate bound a saved listening speed (the
	// table's CHECK says the same).
	MinPlaybackRate = 0.5
	MaxPlaybackRate = 3.0
	// maxBookmarksPerBook caps one user's bookmarks in one book.
	maxBookmarksPerBook = 1000
	// maxBookmarkNoteRunes matches the table's char_length CHECK.
	maxBookmarkNoteRunes = 500
)

// AudiobookStore is what the handler reads and writes. *gen.Queries satisfies
// it.
type AudiobookStore interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	GetAudiobookRate(ctx context.Context, arg gen.GetAudiobookRateParams) (float32, error)
	GetLatestAudiobookRate(ctx context.Context, userID uuid.UUID) (float32, error)
	SetAudiobookRate(ctx context.Context, arg gen.SetAudiobookRateParams) error
	ListAudiobookBookmarks(ctx context.Context, arg gen.ListAudiobookBookmarksParams) ([]gen.ListAudiobookBookmarksRow, error)
	CreateAudiobookBookmark(ctx context.Context, arg gen.CreateAudiobookBookmarkParams) (gen.CreateAudiobookBookmarkRow, error)
	UpdateAudiobookBookmarkNote(ctx context.Context, arg gen.UpdateAudiobookBookmarkNoteParams) (int64, error)
	DeleteAudiobookBookmark(ctx context.Context, arg gen.DeleteAudiobookBookmarkParams) (int64, error)
}

// AudiobookHandler serves /items/{id}/playback-rate, /items/{id}/bookmarks
// and /bookmarks/{id}.
type AudiobookHandler struct {
	store  AudiobookStore
	access LibraryAccessChecker
	logger *slog.Logger
}

// NewAudiobookHandler constructs the handler. access gates every item route
// on library access and the content-rating ceiling, like lists and
// favorites; nil skips the library check (tests).
func NewAudiobookHandler(store AudiobookStore, access LibraryAccessChecker, logger *slog.Logger) *AudiobookHandler {
	return &AudiobookHandler{store: store, access: access, logger: logger}
}

// PlaybackRateResponse is the speed a book plays at. Source says where it
// came from: "book" (set on this book), "recent" (the user's latest speed on
// another book) or "default" (1.0, nothing set yet).
type PlaybackRateResponse struct {
	Rate   float64 `json:"rate"`
	Source string  `json:"source"`
}

// BookmarkJSON is one bookmark. ItemID is the playable item the position is
// in (the book itself for a single-file book, else a chapter); ItemTitle and
// ItemIndex name that chapter.
type BookmarkJSON struct {
	ID         string    `json:"id"`
	ItemID     string    `json:"item_id"`
	ItemTitle  string    `json:"item_title"`
	ItemIndex  *int32    `json:"item_index,omitempty"`
	PositionMS int64     `json:"position_ms"`
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
}

// resolveBook loads the item named by {id}, checks the caller may see it (and
// its book), and returns it with its book's id: the item itself for an
// audiobook, its parent for a chapter. Anything else is not an audiobook.
func (h *AudiobookHandler) resolveBook(w http.ResponseWriter, r *http.Request) (gen.GetMediaItemRow, uuid.UUID, bool) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return gen.GetMediaItemRow{}, uuid.Nil, false
	}
	item, err := h.store.GetMediaItem(r.Context(), id)
	if err != nil {
		respond.NotFound(w, r)
		return gen.GetMediaItemRow{}, uuid.Nil, false
	}
	if !itemAddAllowed(w, r, h.access, h.logger, item) {
		return gen.GetMediaItemRow{}, uuid.Nil, false
	}
	switch item.Type {
	case "audiobook":
		return item, item.ID, true
	case "audiobook_chapter":
		if !item.ParentID.Valid {
			respond.NotFound(w, r)
			return gen.GetMediaItemRow{}, uuid.Nil, false
		}
		book, err := h.store.GetMediaItem(r.Context(), item.ParentID.Bytes)
		if err != nil || book.Type != "audiobook" {
			respond.NotFound(w, r)
			return gen.GetMediaItemRow{}, uuid.Nil, false
		}
		if !itemAddAllowed(w, r, h.access, h.logger, book) {
			return gen.GetMediaItemRow{}, uuid.Nil, false
		}
		return item, book.ID, true
	default:
		respond.ValidationError(w, r, "listening speed and bookmarks are for audiobooks")
		return gen.GetMediaItemRow{}, uuid.Nil, false
	}
}

// GetPlaybackRate handles GET /api/v1/items/{id}/playback-rate ({id} is a
// book or one of its chapters).
func (h *AudiobookHandler) GetPlaybackRate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	_, bookID, ok := h.resolveBook(w, r)
	if !ok {
		return
	}
	rate, err := h.store.GetAudiobookRate(r.Context(), gen.GetAudiobookRateParams{UserID: claims.UserID, BookID: bookID})
	if err == nil {
		respond.Success(w, r, PlaybackRateResponse{Rate: roundRate(float64(rate)), Source: "book"})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.logger.ErrorContext(r.Context(), "get playback rate", "book_id", bookID, "err", err)
		respond.InternalError(w, r)
		return
	}
	rate, err = h.store.GetLatestAudiobookRate(r.Context(), claims.UserID)
	switch {
	case err == nil:
		respond.Success(w, r, PlaybackRateResponse{Rate: roundRate(float64(rate)), Source: "recent"})
	case errors.Is(err, pgx.ErrNoRows):
		respond.Success(w, r, PlaybackRateResponse{Rate: 1, Source: "default"})
	default:
		h.logger.ErrorContext(r.Context(), "get latest playback rate", "err", err)
		respond.InternalError(w, r)
	}
}

// SetPlaybackRate handles PUT /api/v1/items/{id}/playback-rate. Body:
// {"rate": 1.5}, between 0.5 and 3.0.
func (h *AudiobookHandler) SetPlaybackRate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		Rate *float64 `json:"rate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Rate == nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	rate := *body.Rate
	if math.IsNaN(rate) || rate < MinPlaybackRate || rate > MaxPlaybackRate {
		respond.BadRequest(w, r, "rate must be between 0.5 and 3.0")
		return
	}
	_, bookID, ok := h.resolveBook(w, r)
	if !ok {
		return
	}
	if err := h.store.SetAudiobookRate(r.Context(), gen.SetAudiobookRateParams{
		UserID: claims.UserID, BookID: bookID, PlaybackRate: float32(roundRate(rate)),
	}); err != nil {
		h.logger.ErrorContext(r.Context(), "set playback rate", "book_id", bookID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}

// roundRate keeps a speed to two decimals, so the float32 column reads back
// as 1.25, not 1.2500000476837158.
func roundRate(rate float64) float64 {
	return math.Round(rate*100) / 100
}

// ListBookmarks handles GET /api/v1/items/{id}/bookmarks: every bookmark the
// caller has in the book ({id} is the book or one of its chapters), in
// listening order.
func (h *AudiobookHandler) ListBookmarks(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	_, bookID, ok := h.resolveBook(w, r)
	if !ok {
		return
	}
	rows, err := h.store.ListAudiobookBookmarks(r.Context(), gen.ListAudiobookBookmarksParams{UserID: claims.UserID, BookID: bookID})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list bookmarks", "book_id", bookID, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]BookmarkJSON, 0, len(rows))
	for _, b := range rows {
		// item_index is a chapter number. In a single-file book the item is
		// the book, whose own index is its place in a series: leave it out,
		// as CreateBookmark does.
		index := b.ItemIndex
		if b.ItemID == bookID {
			index = nil
		}
		out = append(out, BookmarkJSON{
			ID: b.ID.String(), ItemID: b.ItemID.String(), ItemTitle: b.ItemTitle, ItemIndex: index,
			PositionMS: b.PositionMs, Note: b.Note, CreatedAt: b.CreatedAt.Time,
		})
	}
	respond.Success(w, r, out)
}

// CreateBookmark handles POST /api/v1/items/{id}/bookmarks, where {id} is the
// playable item (a single-file book or a chapter). Body:
// {"position_ms": 123000, "note": "optional"}. 409 BOOKMARK_LIMIT past 1000
// in one book.
func (h *AudiobookHandler) CreateBookmark(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body struct {
		PositionMS *int64 `json:"position_ms"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PositionMS == nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	if *body.PositionMS < 0 {
		respond.BadRequest(w, r, "position_ms must not be negative")
		return
	}
	note, ok := cleanBookmarkNote(w, r, body.Note)
	if !ok {
		return
	}
	item, bookID, ok := h.resolveBook(w, r)
	if !ok {
		return
	}
	row, err := h.store.CreateAudiobookBookmark(r.Context(), gen.CreateAudiobookBookmarkParams{
		UserID: claims.UserID, BookID: bookID, ItemID: item.ID,
		PositionMs: *body.PositionMS, Note: note, MaxPerBook: maxBookmarksPerBook,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		respond.Error(w, r, http.StatusConflict, "BOOKMARK_LIMIT", "this book already has the most bookmarks allowed (1000)")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create bookmark", "item_id", item.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	var index *int32
	if item.ID != bookID {
		index = item.Index
	}
	respond.Created(w, r, BookmarkJSON{
		ID: row.ID.String(), ItemID: item.ID.String(), ItemTitle: item.Title, ItemIndex: index,
		PositionMS: *body.PositionMS, Note: note, CreatedAt: row.CreatedAt.Time,
	})
}

// UpdateBookmark handles PATCH /api/v1/bookmarks/{id}. Body: {"note": "..."}
// ("" clears it). Only the caller's own bookmarks; anyone else's is a 404.
func (h *AudiobookHandler) UpdateBookmark(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid bookmark id")
		return
	}
	var body struct {
		Note *string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Note == nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	note, ok := cleanBookmarkNote(w, r, *body.Note)
	if !ok {
		return
	}
	n, err := h.store.UpdateAudiobookBookmarkNote(r.Context(), gen.UpdateAudiobookBookmarkNoteParams{
		ID: id, UserID: claims.UserID, Note: note,
	})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "update bookmark", "bookmark_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if n == 0 {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// DeleteBookmark handles DELETE /api/v1/bookmarks/{id} (the caller's own).
func (h *AudiobookHandler) DeleteBookmark(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid bookmark id")
		return
	}
	n, err := h.store.DeleteAudiobookBookmark(r.Context(), gen.DeleteAudiobookBookmarkParams{ID: id, UserID: claims.UserID})
	if err != nil {
		h.logger.ErrorContext(r.Context(), "delete bookmark", "bookmark_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	if n == 0 {
		respond.NotFound(w, r)
		return
	}
	respond.NoContent(w)
}

// cleanBookmarkNote trims a note and enforces its length.
func cleanBookmarkNote(w http.ResponseWriter, r *http.Request, note string) (string, bool) {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > maxBookmarkNoteRunes {
		respond.BadRequest(w, r, "note must be at most 500 characters")
		return "", false
	}
	return note, true
}
