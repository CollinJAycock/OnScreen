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
	"github.com/onscreen/onscreen/internal/audit"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/domain/library"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/observability"
	"github.com/onscreen/onscreen/internal/trickplay"
)

// TrickplayPlanner is the automatic-generation surface the handler uses:
// per-library queueing and progress for the admin endpoints, plus the
// shared worker queue the per-item regenerate goes through. Satisfied by
// *trickplay.Planner.
type TrickplayPlanner interface {
	QueueLibrary(ctx context.Context, libraryID uuid.UUID, includeFailed bool) (int, error)
	LibraryCounts(ctx context.Context, libraryID uuid.UUID) (trickplay.LibraryCounts, error)
	QueueItem(itemID uuid.UUID)
}

// TrickplayLibraryLookup resolves a library (existence + its toggle) for the
// per-library endpoints. Satisfied by *library.Service.
type TrickplayLibraryLookup interface {
	Get(ctx context.Context, id uuid.UUID) (*library.Library, error)
}

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

	// planner, when wired, routes the per-item regenerate through the
	// process-wide generation queue (one ffmpeg sprite job at a time,
	// shared with post-scan and backfill generation) and backs the
	// per-library endpoints. nil keeps the standalone path above.
	planner   TrickplayPlanner
	libraries TrickplayLibraryLookup
	audit     *audit.Logger
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

// WithPlanner wires automatic generation: the per-library admin endpoints
// and routing the per-item regenerate through the shared worker queue.
func (h *TrickplayHandler) WithPlanner(p TrickplayPlanner, libs TrickplayLibraryLookup) *TrickplayHandler {
	h.planner = p
	h.libraries = libs
	return h
}

// WithAudit records admin "Generate now" requests in the audit log, like
// the library scan action.
func (h *TrickplayHandler) WithAudit(a *audit.Logger) *TrickplayHandler {
	h.audit = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired.
// A nil checker fails OPEN (serves every library), so api.Handlers.
// ValidateLibraryAccess asserts this at startup and refuses to boot
// without it.
func (h *TrickplayHandler) LibraryAccessWired() bool { return h.access != nil }

// TrickplayStatusJSON is the response body for GET /items/{id}/trickplay.
type TrickplayStatusJSON struct {
	Status      string `json:"status"` // "not_started" | "pending" | "done" | "failed" | "skipped"
	SpriteCount int    `json:"sprite_count,omitempty"`
	IntervalSec int    `json:"interval_sec,omitempty"`
	ThumbWidth  int    `json:"thumb_width,omitempty"`
	ThumbHeight int    `json:"thumb_height,omitempty"`
	LastError   string `json:"last_error,omitempty"`
}

// trickplayStatusBody is the wire shape of the per-item status and generate
// responses: the standard {"data": …} envelope every client parses (the web
// api client and both Android apps read `.data`, and the OpenAPI spec
// documents it) PLUS the same fields flattened at the top level, which is
// all these endpoints used to return. Keeping both makes the envelope fix
// additive — a caller that learned the bare shape keeps working — while the
// clients that expected the envelope finally see real statuses instead of
// falling back to "not_started" (which hid every generated sprite sheet).
type trickplayStatusBody struct {
	TrickplayStatusJSON
	Data TrickplayStatusJSON `json:"data"`
}

func writeTrickplayStatus(w http.ResponseWriter, r *http.Request, code int, s TrickplayStatusJSON) {
	respond.JSON(w, r, code, trickplayStatusBody{TrickplayStatusJSON: s, Data: s})
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
	writeTrickplayStatus(w, r, http.StatusOK, out)
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

	// With automatic generation wired, the regenerate joins the shared
	// worker queue (at the head) instead of spawning its own ffmpeg: one
	// sprite job at a time process-wide, and no chance of this run and a
	// post-scan run of the same item stomping each other's output dir.
	if h.planner != nil {
		h.planner.QueueItem(id)
		writeTrickplayStatus(w, r, http.StatusAccepted, TrickplayStatusJSON{Status: "pending"})
		return
	}

	// Dedup: if a generation for this item is already running, just
	// return 202 — the caller will see "pending" via Status. Without
	// this, an admin POST loop on the same item would queue N copies
	// behind the semaphore, all stomping each other's output dir.
	h.genInFlightMu.Lock()
	if _, running := h.genInFlight[id]; running {
		h.genInFlightMu.Unlock()
		writeTrickplayStatus(w, r, http.StatusAccepted, TrickplayStatusJSON{Status: "pending"})
		return
	}
	h.genInFlight[id] = struct{}{}
	h.genInFlightMu.Unlock()

	// Detached context: the HTTP request ctx will cancel when we return the
	// 202. Generation takes seconds to minutes, so it must outlive the call.
	// Bounded by genSlots — a buffered channel acts as an N-permit
	// semaphore so an admin POST loop can't pin every CPU core with
	// concurrent ffmpeg sprite jobs.
	// SafeGo — Generate shells out to ffmpeg over user-owned media; a panic
	// in the codec layer shouldn't take the server down. The in-flight-
	// map cleanup and the semaphore release both run via defer regardless.
	itemID := id
	observability.SafeGo(h.logger, "trickplay:generate", func() {
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
	})

	writeTrickplayStatus(w, r, http.StatusAccepted, TrickplayStatusJSON{Status: "pending"})
}

// TrickplayLibraryStatusJSON is the response for
// GET /api/v1/libraries/{id}/trickplay/status. Counts cover the library's
// video items that have a playable file; pending = not yet generated
// (including in progress), failed includes items skipped for having no
// usable file.
type TrickplayLibraryStatusJSON struct {
	Enabled bool  `json:"enabled"`
	Total   int64 `json:"total"`
	Done    int64 `json:"done"`
	Pending int64 `json:"pending"`
	Failed  int64 `json:"failed"`
}

// LibraryStatus handles GET /api/v1/libraries/{id}/trickplay/status.
// Admin-only (router admin group; re-checked here).
func (h *TrickplayHandler) LibraryStatus(w http.ResponseWriter, r *http.Request) {
	lib, ok := h.requireAdminLibrary(w, r)
	if !ok {
		return
	}
	c, err := h.planner.LibraryCounts(r.Context(), lib.ID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "trickplay library status", "library_id", lib.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, TrickplayLibraryStatusJSON{
		Enabled: lib.TrickplayEnabled,
		Total:   c.Total,
		Done:    c.Done,
		Pending: c.Pending,
		Failed:  c.Failed,
	})
}

// GenerateLibrary handles POST /api/v1/libraries/{id}/trickplay/generate.
// Admin-only. Queues every item in the library that has no sprites yet on
// the shared generation worker and returns 202 immediately; idempotent —
// items already queued or generating aren't added again. Works whether or
// not the library's automatic toggle is on (it's an explicit request).
// ?include_failed=true also retries items whose last attempt failed.
func (h *TrickplayHandler) GenerateLibrary(w http.ResponseWriter, r *http.Request) {
	lib, ok := h.requireAdminLibrary(w, r)
	if !ok {
		return
	}
	includeFailed := r.URL.Query().Get("include_failed") == "true"
	n, err := h.planner.QueueLibrary(r.Context(), lib.ID, includeFailed)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "trickplay queue library", "library_id", lib.ID, "err", err)
		respond.InternalError(w, r)
		return
	}
	if h.audit != nil {
		var actor *uuid.UUID
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			a := claims.UserID
			actor = &a
		}
		h.audit.Log(r.Context(), actor, audit.ActionLibraryTrickplay, lib.ID.String(),
			map[string]any{"queued": n, "include_failed": includeFailed}, audit.ClientIP(r))
	}
	respond.Accepted(w, r, map[string]any{"status": "queued", "queued": n})
}

// requireAdminLibrary gates the per-library endpoints: planner wired, admin
// caller, valid + existing library. Writes the response and returns false
// otherwise.
func (h *TrickplayHandler) requireAdminLibrary(w http.ResponseWriter, r *http.Request) (*library.Library, bool) {
	if h.svc == nil || h.planner == nil || h.libraries == nil {
		respond.NotFound(w, r)
		return nil, false
	}
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return nil, false
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid library id")
		return nil, false
	}
	lib, err := h.libraries.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			respond.NotFound(w, r)
			return nil, false
		}
		h.logger.ErrorContext(r.Context(), "trickplay get library", "library_id", id, "err", err)
		respond.InternalError(w, r)
		return nil, false
	}
	return lib, true
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
	// `private`, not `public`: this response is authorized per request (per-
	// library ACL plus the caller's content-rating ceiling) but the browser
	// URL carries no token or user identity and there is no Vary, so a shared
	// cache in front of the app would store one user's authorized sprite and
	// replay it to everyone — including restricted profiles. Matches every
	// sibling ACL-gated asset (subtitles.go, photos.go, books.go). CDN offload
	// for these should go through the PublicAssetCache admin opt-in the
	// artwork path uses, not a hardcoded `public`.
	w.Header().Set("Cache-Control", "private, max-age=86400, must-revalidate")
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
		// Content-rating ceiling. Sprites are downsampled frame grabs of the
		// real video, so library ACL alone would leak over-ceiling visual
		// content to a restricted profile (same gap closed on the artwork
		// server). 404 to keep the absence indistinguishable from a missing item.
		cr := ""
		if item.ContentRating != nil {
			cr = *item.ContentRating
		}
		if !contentrating.IsAllowed(cr, claims.MaxContentRating) {
			respond.NotFound(w, r)
			return uuid.Nil, false
		}
	}
	return id, true
}
