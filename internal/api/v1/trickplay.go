package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// TrickplayService is the subset of the trickplay package the API needs.
// The file-serving path only needs ItemDir + Status; generation runs
// detached so the handler can return 202 immediately.
type TrickplayService interface {
	Status(ctx context.Context, itemID uuid.UUID) (trickplay.Spec, string, int, bool, error)
	Generate(ctx context.Context, itemID uuid.UUID) error
	ItemDir(itemID uuid.UUID) string
}

// TrickplayHandler serves trickplay read/generate endpoints plus the
// on-disk sprite/VTT files. It keeps the handler free of direct DB/gen
// dependencies so items.go doesn't pull in trickplay internals.
type TrickplayHandler struct {
	svc    TrickplayService
	media  TrickplayMediaLookup
	access LibraryAccessChecker
	logger *slog.Logger
	// genSlots bounds concurrent in-flight generations across the
	// process. Each Generate call spawns ffmpeg + image-encoder work
	// at full CPU, so an admin POSTing /generate in a tight loop can
	// pin every core. Buffered channel = N-permit semaphore: take a
	// slot before kicking off, release in the goroutine's defer.
	genSlots chan struct{}
	// genInFlight tracks item IDs currently generating so a flurry of
	// POSTs for the same item collapses to one job (idempotent re-
	// trigger). Keyed by item UUID; entries cleared when the goroutine
	// exits.
	genInFlight   map[uuid.UUID]struct{}
	genInFlightMu sync.Mutex
}

// MaxConcurrentTrickplayGenerations bounds in-flight ffmpeg sprite jobs
// across the process. 2 covers typical admin "regenerate everything"
// batches without saturating CPU; tune via env if needed.
const MaxConcurrentTrickplayGenerations = 2

// TrickplayMediaLookup is the minimal media interface the handler uses to
// resolve an item's library for access checks. Satisfied by media.Service.
type TrickplayMediaLookup interface {
	GetItem(ctx context.Context, id uuid.UUID) (*media.Item, error)
}

// NewTrickplayHandler wires a handler. svc may be nil — the handler then
// returns 404 for all trickplay routes, which matches how WithMarkers works.
func NewTrickplayHandler(svc TrickplayService, media TrickplayMediaLookup, logger *slog.Logger) *TrickplayHandler {
	return &TrickplayHandler{
		svc:         svc,
		media:       media,
		logger:      logger,
		genSlots:    make(chan struct{}, MaxConcurrentTrickplayGenerations),
		genInFlight: make(map[uuid.UUID]struct{}),
	}
}

// WithLibraryAccess enforces per-library ACLs on trickplay reads.
func (h *TrickplayHandler) WithLibraryAccess(a LibraryAccessChecker) *TrickplayHandler {
	h.access = a
	return h
}

// TrickplayStatusJSON is the response body for GET /items/{id}/trickplay.
type TrickplayStatusJSON struct {
	Status      string `json:"status"` // "not_started" | "pending" | "done" | "failed" | "skipped"
	SpriteCount int    `json:"sprite_count,omitempty"`
	IntervalSec int    `json:"interval_sec,omitempty"`
	ThumbWidth  int    `json:"thumb_width,omitempty"`
	ThumbHeight int    `json:"thumb_height,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// Status handles GET /api/v1/items/{id}/trickplay. Returns status_not_started
// when there's no row yet so clients can render "Not generated" consistently.
func (h *TrickplayHandler) Status(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		respond.NotFound(w, r)
		return
	}
	id, ok := h.requireItemAccess(w, r)
	if !ok {
		return
	}
	spec, status, spriteCount, exists, err := h.svc.Status(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "trickplay status", "id", id, "err", err)
		respond.InternalError(w, r)
		return
	}
	out := TrickplayStatusJSON{Status: "not_started"}
	if exists {
		out.Status = status
		out.SpriteCount = spriteCount
		out.IntervalSec = spec.IntervalSec
		out.ThumbWidth = spec.ThumbWidth
		out.ThumbHeight = spec.ThumbHeight
	}
	respond.JSON(w, r, http.StatusOK, out)
}

// Generate handles POST /api/v1/items/{id}/trickplay. Admin-only. Fires the
// generator in a detached goroutine and returns 202 with the current status.
func (h *TrickplayHandler) Generate(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		respond.NotFound(w, r)
		return
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	id, ok := h.requireItemAccess(w, r)
	if !ok {
		return
	}

	// Dedup: if a generation for this item is already running, just
	// return 202 — the caller will see "pending" via Status. Without
	// this, an admin POST loop on the same item would queue N copies
	// behind the semaphore, all stomping each other's output dir.
	h.genInFlightMu.Lock()
	if _, running := h.genInFlight[id]; running {
		h.genInFlightMu.Unlock()
		respond.JSON(w, r, http.StatusAccepted, TrickplayStatusJSON{Status: "pending"})
		return
	}
	h.genInFlight[id] = struct{}{}
	h.genInFlightMu.Unlock()

	// Detached context: the HTTP request ctx will cancel when we return the
	// 202. Generation takes seconds to minutes, so it must outlive the call.
	// Bounded by genSlots — a buffered channel acts as an N-permit
	// semaphore so an admin POST loop can't pin every CPU core with
	// concurrent ffmpeg sprite jobs.
	go func(itemID uuid.UUID) {
		defer func() {
			h.genInFlightMu.Lock()
			delete(h.genInFlight, itemID)
			h.genInFlightMu.Unlock()
		}()
		h.genSlots <- struct{}{}
		defer func() { <-h.genSlots }()
		bg := context.Background()
		if err := h.svc.Generate(bg, itemID); err != nil {
			h.logger.Error("trickplay generate", "id", itemID, "err", err)
		}
	}(id)

	respond.JSON(w, r, http.StatusAccepted, TrickplayStatusJSON{Status: "pending"})
}

// trickplayFilePattern restricts /trickplay/{id}/* to known filenames so a
// bad path component can't escape the item's directory.
var trickplayFilePattern = regexp.MustCompile(`^(index\.vtt|sprite_\d{3}\.jpg)$`)

// ServeFile handles GET /trickplay/{id}/{file}. Requires auth + library
// ACL — sprites can leak adult-library thumbnails into a kids-restricted
// session if served openly. The route is wrapped in Auth_mw.Required by
// the router; this handler additionally enforces the per-library ACL.
// The filename is whitelisted to index.vtt or sprite_NNN.jpg.
func (h *TrickplayHandler) ServeFile(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		http.NotFound(w, r)
		return
	}
	id, ok := h.requireItemAccess(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "file")
	if !trickplayFilePattern.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(h.svc.ItemDir(id), name)
	if _, err := os.Stat(full); err != nil {
		http.NotFound(w, r)
		return
	}
	// Long cache — regeneration writes to a fresh directory anyway and the
	// VTT cues bust themselves via the item's updated_at query param when
	// the client embeds one.
	if filepath.Ext(name) == ".vtt" {
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	}
	w.Header().Set("Cache-Control", "public, max-age=86400, must-revalidate")
	http.ServeFile(w, r, full)
}

// requireItemAccess parses {id}, loads the item, and enforces library ACLs.
// Returns the id and true when the caller may proceed; otherwise writes a
// response and returns false.
func (h *TrickplayHandler) requireItemAccess(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		respond.BadRequest(w, r, "invalid item id")
		return uuid.Nil, false
	}
	item, err := h.media.GetItem(r.Context(), id)
	if err != nil {
		if errors.Is(err, media.ErrNotFound) {
			respond.NotFound(w, r)
			return uuid.Nil, false
		}
		h.logger.ErrorContext(r.Context(), "trickplay get item", "id", id, "err", err)
		respond.InternalError(w, r)
		return uuid.Nil, false
	}
	if h.access != nil {
		claims := middleware.ClaimsFromContext(r.Context())
		if claims == nil {
			respond.Forbidden(w, r)
			return uuid.Nil, false
		}
		ok, err := h.access.CanAccessLibrary(r.Context(), claims.UserID, item.LibraryID, claims.IsAdmin)
		if err != nil {
			respond.InternalError(w, r)
			return uuid.Nil, false
		}
		if !ok {
			respond.NotFound(w, r)
			return uuid.Nil, false
		}
	}
	return id, true
}
