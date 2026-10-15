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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ReadingProgressDB is the subset of the generated queries the reading
// progress endpoints need. Satisfied by *gen.Queries.
type ReadingProgressDB interface {
	GetMediaItem(ctx context.Context, id uuid.UUID) (gen.GetMediaItemRow, error)
	GetReadingProgress(ctx context.Context, arg gen.GetReadingProgressParams) (gen.ReadingProgress, error)
	UpsertReadingProgress(ctx context.Context, arg gen.UpsertReadingProgressParams) (int64, error)
	MarkItemRead(ctx context.Context, arg gen.MarkItemReadParams) error
}

// readFinishedAt is the progression at which a book counts as read: the last
// page or two of an EPUB rarely reports exactly 1.0 (epub.js reports the
// start of the last screen), and back matter is skipped.
const readFinishedAt = 0.98

// Bounds on what a client may store. A CFI for a deep EPUB node runs to a few
// hundred characters; 4 KiB leaves room without letting the row become a
// free-form blob store.
const (
	maxReadingLocatorLen = 4096
	maxReadingDeviceLen  = 100
)

// ReadingProgressHandler serves GET / PUT /api/v1/items/{id}/reading-progress:
// where the caller is in an ebook (EPUB, CBZ, CBR, PDF), so opening it on
// another device picks up there. Per user; the item must be a book the caller
// can see (library access, rating ceiling, label restriction — 404
// otherwise). Audiobooks keep their playback progress and don't use this.
type ReadingProgressHandler struct {
	db     ReadingProgressDB
	access LibraryAccessChecker
	logger *slog.Logger
	now    func() time.Time
}

// NewReadingProgressHandler creates a ReadingProgressHandler.
func NewReadingProgressHandler(db ReadingProgressDB, access LibraryAccessChecker, logger *slog.Logger) *ReadingProgressHandler {
	return &ReadingProgressHandler{db: db, access: access, logger: logger, now: time.Now}
}

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *ReadingProgressHandler) LibraryAccessWired() bool { return h.access != nil }

// readingProgressJSON is the stored position. updated_at is the client clock
// the position was saved under (clamped to the server's), which is what
// "newest wins" compares; device_name says where it was saved.
type readingProgressJSON struct {
	Locator     string    `json:"locator"`
	Progression float64   `json:"progression"`
	DeviceName  string    `json:"device_name,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	// Applied, on a PUT only: false when the server kept a newer position
	// than the one sent (returned here instead).
	Applied *bool `json:"applied,omitempty"`
}

func toReadingProgressJSON(p gen.ReadingProgress) readingProgressJSON {
	return readingProgressJSON{
		Locator:     p.Locator,
		Progression: p.Progression,
		DeviceName:  derefStr(p.DeviceName),
		UpdatedAt:   p.ClientUpdatedAt.Time,
	}
}

// book loads the item and applies the visibility gate. Anything that isn't a
// visible book is a 404, so the endpoint can't be used to probe other items.
func (h *ReadingProgressHandler) book(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := parseUUID(r, "id")
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return uuid.Nil, false
	}
	item, err := h.db.GetMediaItem(r.Context(), id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			h.logger.ErrorContext(r.Context(), "reading progress: get item", "item_id", id, "err", err)
			respond.InternalError(w, r)
			return uuid.Nil, false
		}
		respond.NotFound(w, r)
		return uuid.Nil, false
	}
	if !itemAddAllowed(w, r, h.access, h.logger, item) {
		return uuid.Nil, false
	}
	if item.Type != "book" {
		respond.NotFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}

// Get handles GET /api/v1/items/{id}/reading-progress. data is null when the
// caller has no saved position for the book.
func (h *ReadingProgressHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, ok := h.book(w, r)
	if !ok {
		return
	}
	p, err := h.db.GetReadingProgress(r.Context(), gen.GetReadingProgressParams{UserID: claims.UserID, MediaID: id})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			respond.Success(w, r, nil)
			return
		}
		h.logger.ErrorContext(r.Context(), "reading progress: get", "item_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, toReadingProgressJSON(p))
}

// putReadingProgressRequest is the PUT body. updated_at is the client's clock
// when the position was reached; omitted means now. A time ahead of the
// server's clock is clamped to it, so a device with a fast clock can't pin
// its position against every later save.
type putReadingProgressRequest struct {
	Locator     string     `json:"locator"`
	Progression *float64   `json:"progression"`
	DeviceName  string     `json:"device_name"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

// Put handles PUT /api/v1/items/{id}/reading-progress. The newest position by
// client time wins; the response is whichever position the server now holds.
// Reaching readFinishedAt marks the book read (the same mark POST
// /items/{id}/watched makes).
func (h *ReadingProgressHandler) Put(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	id, ok := h.book(w, r)
	if !ok {
		return
	}
	var body putReadingProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body")
		return
	}
	body.Locator = strings.TrimSpace(body.Locator)
	if body.Locator == "" || len(body.Locator) > maxReadingLocatorLen {
		respond.ValidationError(w, r, "locator is required and must be at most 4096 bytes")
		return
	}
	if body.Progression == nil || math.IsNaN(*body.Progression) || *body.Progression < 0 || *body.Progression > 1 {
		respond.ValidationError(w, r, "progression must be between 0 and 1")
		return
	}
	device := strings.TrimSpace(body.DeviceName)
	if utf8.RuneCountInString(device) > maxReadingDeviceLen {
		device = string([]rune(device)[:maxReadingDeviceLen])
	}
	now := h.now()
	at := now
	if body.UpdatedAt != nil && !body.UpdatedAt.IsZero() && body.UpdatedAt.Before(now) {
		at = *body.UpdatedAt
	}
	var devicePtr *string
	if device != "" {
		devicePtr = &device
	}
	ctx := r.Context()
	n, err := h.db.UpsertReadingProgress(ctx, gen.UpsertReadingProgressParams{
		UserID:          claims.UserID,
		MediaID:         id,
		Locator:         body.Locator,
		Progression:     *body.Progression,
		DeviceName:      devicePtr,
		ClientUpdatedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
	if err != nil {
		h.logger.ErrorContext(ctx, "reading progress: save", "item_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	// Reaching the end on any device finishes the book, even when a newer
	// position from elsewhere kept this one from being stored.
	if *body.Progression >= readFinishedAt {
		if err := h.db.MarkItemRead(ctx, gen.MarkItemReadParams{UserID: claims.UserID, MediaID: id}); err != nil {
			h.logger.WarnContext(ctx, "reading progress: mark read", "item_id", id, "err", err)
		}
	}
	stored, err := h.db.GetReadingProgress(ctx, gen.GetReadingProgressParams{UserID: claims.UserID, MediaID: id})
	if err != nil {
		h.logger.ErrorContext(ctx, "reading progress: reload", "item_id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := toReadingProgressJSON(stored)
	applied := n > 0
	out.Applied = &applied
	respond.Success(w, r, out)
}
