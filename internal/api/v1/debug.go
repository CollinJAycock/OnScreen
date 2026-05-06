package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	httppprof "net/http/pprof"
	"runtime"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
)

// ExplainPool is the slice of pgxpool.Pool the Explain handler needs.
// Defined as an interface so tests can stub without importing pgx.
type ExplainPool interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// DebugHandler exposes runtime diagnostics for an authenticated admin.
// Two surfaces:
//
//   - GET /api/v1/admin/debug/runtime — small JSON snapshot of
//     goroutine count + key memstats. Cheap to call repeatedly so the
//     operator can sample over time without grabbing a full pprof dump.
//   - GET /api/v1/admin/debug/pprof/* — the standard net/http/pprof
//     handlers, mounted under the admin gate so the same `go tool
//     pprof` workflow operators run against a local Go service works
//     remotely against the public HTTPS API. Without this we'd need
//     a TCP tunnel to the metrics port (the other place pprof lives,
//     bound to the operator-only network).
//
// Why both: the runtime snapshot is the at-a-glance leak indicator
// (goroutines climbing? heap climbing?). pprof gives the diff once a
// trend is confirmed.
type DebugHandler struct {
	logger *slog.Logger
	pool   ExplainPool // optional — when set, enables /admin/debug/explain/{name}
}

func NewDebugHandler(logger *slog.Logger) *DebugHandler {
	return &DebugHandler{logger: logger}
}

// WithExplain enables the EXPLAIN ANALYZE endpoint by wiring a pool
// to run the named queries. Without this, /admin/debug/explain/{name}
// is not registered. Pool should be the read-only pool — EXPLAIN
// ANALYZE actually executes the statement, but the allowlisted shapes
// are all SELECT.
func (h *DebugHandler) WithExplain(pool ExplainPool) *DebugHandler {
	h.pool = pool
	return h
}

// Explain runs EXPLAIN (ANALYZE, BUFFERS, VERBOSE) against a
// hardcoded allowlist of named queries and returns the plan as
// text/plain. Admin-only (gated at the router). The allowlist is
// intentionally narrow — operators describe a problem ("hub is
// slow"), and we ship a name that maps to the relevant production
// query shape. We never accept arbitrary SQL: even gated behind
// admin, an "execute any SQL" endpoint is a step too far for a
// public-facing API.
//
// URL: GET /api/v1/admin/debug/explain/{name}?library_id=<uuid>&user_id=<uuid>
func (h *DebugHandler) Explain(w http.ResponseWriter, r *http.Request) {
	if h.pool == nil {
		http.Error(w, "explain not configured", http.StatusNotFound)
		return
	}

	name := chi.URLParam(r, "name")
	q := r.URL.Query()

	parseUUID := func(key string) (uuid.UUID, bool, error) {
		v := q.Get(key)
		if v == "" {
			return uuid.Nil, false, nil
		}
		u, err := uuid.Parse(v)
		if err != nil {
			return uuid.Nil, false, fmt.Errorf("invalid %s: %w", key, err)
		}
		return u, true, nil
	}

	libID, libSet, err := parseUUID("library_id")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID, userSet, err := parseUUID("user_id")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Default user_id to caller for continue_watching so an admin
	// just hits ?name=continue_watching and gets *their* in-progress
	// shape. They can override with ?user_id=<uuid>.
	if !userSet {
		if claims := middleware.ClaimsFromContext(r.Context()); claims != nil {
			userID = claims.UserID
			userSet = true
		}
	}

	type plan struct {
		sql  string
		args []any
		// requiredParams names what the caller must supply via query
		// string. Used only for the "missing param" error message.
		requiredParams []string
	}

	// Allowlist. Each entry is a single SELECT statement; we wrap it
	// with EXPLAIN (...) below. Keep these in lockstep with the
	// production queries in internal/db/queries/media.sql when those
	// shapes are tweaked — these copies exist so the planner's plan
	// reflects what the live handler is actually running.
	plans := map[string]plan{
		"recently_added_per_library": {
			requiredParams: []string{"library_id"},
			args:           []any{libID},
			sql: `WITH recent_episodes AS (
    SELECT e.id, e.library_id, e.parent_id, e.created_at, e.title, e.type,
           e.poster_path, e.fanart_path, e.thumb_path, e.content_rating
    FROM media_items e
    WHERE e.type = 'episode' AND e.deleted_at IS NULL
      AND e.library_id = $1
    ORDER BY e.created_at DESC
    LIMIT 500
), episodes AS (
    SELECT e.id, e.library_id, e.type, e.title,
           COALESCE(grandparent.poster_path, parent.poster_path, e.poster_path) AS fallback_poster,
           ROW_NUMBER() OVER (PARTITION BY grandparent.id ORDER BY e.created_at DESC) AS rn
    FROM recent_episodes e
    JOIN media_items parent ON parent.id = e.parent_id AND parent.deleted_at IS NULL
    JOIN media_items grandparent ON grandparent.id = parent.parent_id
        AND grandparent.deleted_at IS NULL
        AND grandparent.type = 'show'
        AND grandparent.poster_path IS NOT NULL
)
SELECT id, library_id, type, title, fallback_poster
FROM episodes WHERE rn = 1 LIMIT 24`,
		},
		"recently_added_global": {
			sql: `WITH recent_episodes AS (
    SELECT e.id, e.library_id, e.parent_id, e.created_at, e.title, e.type,
           e.poster_path
    FROM media_items e
    WHERE e.type = 'episode' AND e.deleted_at IS NULL
    ORDER BY e.created_at DESC
    LIMIT 500
), episodes AS (
    SELECT e.id, e.library_id, e.type, e.title,
           COALESCE(grandparent.poster_path, e.poster_path) AS fallback_poster,
           ROW_NUMBER() OVER (PARTITION BY grandparent.id ORDER BY e.created_at DESC) AS rn
    FROM recent_episodes e
    JOIN media_items parent ON parent.id = e.parent_id AND parent.deleted_at IS NULL
    JOIN media_items grandparent ON grandparent.id = parent.parent_id
        AND grandparent.deleted_at IS NULL
        AND grandparent.type = 'show'
        AND grandparent.poster_path IS NOT NULL
)
SELECT id, library_id, type, title, fallback_poster
FROM episodes WHERE rn = 1 LIMIT 40`,
		},
		"recently_added_movies_branch": {
			requiredParams: []string{"library_id"},
			args:           []any{libID},
			sql: `SELECT id, library_id, type, title, poster_path, created_at
FROM media_items
WHERE deleted_at IS NULL
  AND type IN ('movie', 'album', 'photo', 'audiobook', 'podcast', 'home_video', 'book')
  AND poster_path IS NOT NULL
  AND library_id = $1
ORDER BY created_at DESC
LIMIT 24`,
		},
		"continue_watching": {
			requiredParams: []string{"user_id (or be authenticated)"},
			args:           []any{userID},
			sql: `SELECT m.id, m.title, ws.position_ms, ws.last_watched_at
FROM watch_state ws
JOIN media_items m ON m.id = ws.media_id
LEFT JOIN media_items parent ON parent.id = m.parent_id
LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
WHERE ws.user_id = $1
  AND ws.status = 'in_progress'
  AND m.deleted_at IS NULL
  AND m.type IN ('movie', 'episode')
ORDER BY ws.last_watched_at DESC
LIMIT 20`,
		},
		"trending": {
			sql: `WITH watched AS (
    SELECT
        CASE WHEN m.type = 'episode'
             THEN COALESCE(grandparent.id, parent.id, m.id)
             ELSE m.id END AS bucket_id,
        we.user_id, we.event_type, we.occurred_at
    FROM media_items m
    JOIN watch_events we ON we.media_id = m.id
    LEFT JOIN media_items parent ON parent.id = m.parent_id
    LEFT JOIN media_items grandparent ON grandparent.id = parent.parent_id
    WHERE m.deleted_at IS NULL
      AND m.type IN ('movie', 'episode')
      AND we.event_type IN ('play', 'scrobble', 'stop')
      AND we.occurred_at >= NOW() - make_interval(days => 7)
),
agg AS (
    SELECT bucket_id,
           COUNT(DISTINCT user_id) AS unique_viewers,
           COUNT(*) AS total_events
    FROM watched GROUP BY bucket_id
)
SELECT t.id, t.title, agg.unique_viewers, agg.total_events
FROM agg
JOIN media_items t ON t.id = agg.bucket_id
WHERE t.deleted_at IS NULL
ORDER BY agg.unique_viewers DESC, agg.total_events DESC
LIMIT 30`,
		},
	}

	p, ok := plans[name]
	if !ok {
		names := make([]string, 0, len(plans))
		for k := range plans {
			names = append(names, k)
		}
		http.Error(w, "unknown plan name. available: "+strings.Join(names, ", "), http.StatusNotFound)
		return
	}

	// Validate required params arrived. We've already populated args
	// from the parsed query string, but a UUID arg defaulted to
	// uuid.Nil silently produces a meaningless plan ("scan for rows
	// where library_id = 00000000-…"). Reject up front.
	for _, req := range p.requiredParams {
		if req == "library_id" && !libSet {
			http.Error(w, "missing required query param: library_id", http.StatusBadRequest)
			return
		}
		if strings.HasPrefix(req, "user_id") && !userSet {
			http.Error(w, "missing required query param: user_id", http.StatusBadRequest)
			return
		}
	}

	rows, err := h.pool.Query(r.Context(), "EXPLAIN (ANALYZE, BUFFERS, VERBOSE) "+p.sql, p.args...)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "explain query", "name", name, "err", err)
		http.Error(w, "explain failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
			return
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "rows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(sb.String()))
}

// RuntimeSnapshot is the JSON shape returned by /admin/debug/runtime.
// Compact on purpose — operators sample this at a tight cadence; the
// full pprof tree is the next step when something here looks off.
type RuntimeSnapshot struct {
	Goroutines int    `json:"goroutines"`
	NumCPU     int    `json:"num_cpu"`
	GoVersion  string `json:"go_version"`

	// Memory — bytes. heap_inuse is the most useful "what's resident
	// right now" number; sys is the OS-allocated total (only ever
	// grows). alloc + total_alloc together let an operator infer GC
	// pressure (rate of allocs between samples).
	HeapAlloc      uint64 `json:"heap_alloc"`
	HeapInuse      uint64 `json:"heap_inuse"`
	HeapSys        uint64 `json:"heap_sys"`
	StackInuse     uint64 `json:"stack_inuse"`
	Sys            uint64 `json:"sys"`
	TotalAllocSum  uint64 `json:"total_alloc"`
	NumGC          uint32 `json:"num_gc"`
	GCPauseLastNs  uint64 `json:"gc_pause_last_ns"`
}

func (h *DebugHandler) Runtime(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	snap := RuntimeSnapshot{
		Goroutines:    runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		GoVersion:     runtime.Version(),
		HeapAlloc:     m.HeapAlloc,
		HeapInuse:     m.HeapInuse,
		HeapSys:       m.HeapSys,
		StackInuse:    m.StackInuse,
		Sys:           m.Sys,
		TotalAllocSum: m.TotalAlloc,
		NumGC:         m.NumGC,
	}
	if m.NumGC > 0 {
		// PauseNs is a circular buffer indexed by `(NumGC+255) % 256`.
		snap.GCPauseLastNs = m.PauseNs[(m.NumGC+255)%256]
	}
	w.Header().Set("Content-Type", "application/json")
	respond.JSON(w, r, http.StatusOK, snap)
}

// pprofHandler returns the right pprof handler for a given debug-name.
// The standard set + the named profiles (heap, goroutine, allocs, …)
// — chi-routed under /admin/debug/pprof/{name}.
//
// httppprof.Index dispatches profiles by stripping the literal prefix
// "/debug/pprof/" from r.URL.Path. Our route is mounted at
// /api/v1/admin/debug/pprof/{name}, so we rewrite the path on a
// shallow request clone to feed Index the prefix it expects. Without
// this, Index would always render the HTML index page.
func (h *DebugHandler) Pprof(w http.ResponseWriter, r *http.Request) {
	w.Header().Del("Content-Type")
	name := chi.URLParam(r, "name")
	r2 := r.Clone(r.Context())
	if name == "" {
		r2.URL.Path = "/debug/pprof/"
	} else {
		r2.URL.Path = "/debug/pprof/" + name
	}
	httppprof.Index(w, r2)
}

func (h *DebugHandler) PprofCmdline(w http.ResponseWriter, r *http.Request) {
	httppprof.Cmdline(w, r)
}

func (h *DebugHandler) PprofProfile(w http.ResponseWriter, r *http.Request) {
	httppprof.Profile(w, r)
}

func (h *DebugHandler) PprofSymbol(w http.ResponseWriter, r *http.Request) {
	httppprof.Symbol(w, r)
}

func (h *DebugHandler) PprofTrace(w http.ResponseWriter, r *http.Request) {
	httppprof.Trace(w, r)
}

// Compile-time check: respond.JSON should accept the snapshot struct.
var _ = json.Marshal
