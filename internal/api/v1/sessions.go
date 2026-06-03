package v1

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// sessionActivityTimeout is how long a transcode session can be inactive
// (no progress heartbeat) before being hidden from "Now Playing". Shares the
// same window as the per-user concurrency cap (transcode.CountByUser), so a
// session that's hidden here is also no longer counted against the cap — one
// definition of "live".
const sessionActivityTimeout = transcode.ActiveSessionWindow

type sessionItemQuerier interface {
	GetMediaItemsForSessions(ctx context.Context, ids []uuid.UUID) ([]gen.SessionMediaItem, error)
	GetMediaItemByFilePath(ctx context.Context, filePath string) (*gen.SessionMediaItem, error)
}

// NativeSessionsHandler handles GET /api/v1/sessions.
type NativeSessionsHandler struct {
	store   *transcode.SessionStore
	tracker *streaming.Tracker
	db      sessionItemQuerier
	logger  *slog.Logger
}

// NewNativeSessionsHandler creates a NativeSessionsHandler.
func NewNativeSessionsHandler(
	store *transcode.SessionStore,
	tracker *streaming.Tracker,
	db sessionItemQuerier,
	logger *slog.Logger,
) *NativeSessionsHandler {
	return &NativeSessionsHandler{store: store, tracker: tracker, db: db, logger: logger}
}

type activeSession struct {
	ID          string  `json:"id"`
	Decision    string  `json:"decision"`
	PositionMS  int64   `json:"position_ms"`
	ClientName  string  `json:"client_name,omitempty"`
	StartedAt   string  `json:"started_at"`
	Title       string  `json:"title"`
	Year        *int    `json:"year,omitempty"`
	Type        string  `json:"type,omitempty"`
	PosterPath  *string `json:"poster_path,omitempty"`
	DurationMS  *int64  `json:"duration_ms,omitempty"`
	BitrateKbps *int    `json:"bitrate_kbps,omitempty"`
	// SelectedRendition is the ABR rung the player's adaptive logic settled on
	// (e.g. "720p"). Empty for non-ABR sessions.
	SelectedRendition *string `json:"selected_rendition,omitempty"`
}

// List handles GET /api/v1/sessions.
func (h *NativeSessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.ClaimsFromContext(ctx)
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	resp := make([]activeSession, 0)

	// ── Transcode/direct-play sessions from Valkey ────────────────────────────
	valkeySessions, err := h.store.List(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "sessions: list valkey", "err", err)
	} else if len(valkeySessions) > 0 {
		ids := make([]uuid.UUID, 0, len(valkeySessions))
		seen := map[uuid.UUID]bool{}
		for _, s := range valkeySessions {
			if !seen[s.MediaItemID] {
				ids = append(ids, s.MediaItemID)
				seen[s.MediaItemID] = true
			}
		}
		items, err := h.db.GetMediaItemsForSessions(ctx, ids)
		if err != nil {
			h.logger.ErrorContext(ctx, "sessions: item lookup", "err", err)
		}
		itemMap := make(map[uuid.UUID]gen.SessionMediaItem, len(items))
		for _, item := range items {
			itemMap[item.ID] = item
		}
		for _, s := range valkeySessions {
			// Filter out sessions with no recent activity. Use LastActivityAt
			// when set; fall back to CreatedAt for brand-new sessions that have
			// not yet received a progress event. Old stale sessions without
			// LastActivityAt will have a zero value, so CreatedAt is used —
			// sessions created more than sessionActivityTimeout ago with no
			// activity are considered stale and hidden.
			// Rung children of an ABR parent are internal — an ABR stream shows
			// as the single parent card, not one per rung.
			if s.ParentID != "" {
				continue
			}
			// Non-admins see only their own sessions; admins get the full
			// now-playing dashboard. The direct-play tracker entries below
			// carry no user id, so they're shown to admins only.
			if !claims.IsAdmin && s.UserID != claims.UserID {
				continue
			}

			activity := s.LastActivityAt
			if activity.IsZero() {
				activity = s.CreatedAt
			}
			if time.Since(activity) > sessionActivityTimeout {
				continue
			}

			as := activeSession{
				ID:         s.ID,
				Decision:   s.Decision,
				PositionMS: s.PositionMS,
				ClientName: s.ClientName,
				StartedAt:  s.CreatedAt.Format("2006-01-02T15:04:05Z"),
				Title:      filepath.Base(s.FilePath),
			}
			if s.BitrateKbps > 0 {
				br := s.BitrateKbps
				as.BitrateKbps = &br
			}
			// ABR: surface the rung the player settled on, and report that
			// rung's bitrate (the parent itself runs no encode, so its own
			// BitrateKbps is 0).
			if s.ABR && s.SelectedRendition != "" {
				label := s.SelectedRendition
				as.SelectedRendition = &label
				for _, rd := range s.ABRRenditions {
					if rd.Label == label {
						if rd.BitrateKbps > 0 {
							br := rd.BitrateKbps
							as.BitrateKbps = &br
						}
						break
					}
				}
			}
			if mi, ok := itemMap[s.MediaItemID]; ok {
				as.Title = mi.Title
				as.Type = mi.Type
				if mi.Year.Valid {
					y := int(mi.Year.Int32)
					as.Year = &y
				}
				if mi.PosterPath.Valid {
					as.PosterPath = &mi.PosterPath.String
				}
				if mi.DurationMS.Valid {
					as.DurationMS = &mi.DurationMS.Int64
				}
			}
			resp = append(resp, as)
		}
	}

	// ── Direct HTTP streams from file-server tracker ──────────────────────────
	// Skip tracker entries whose file path is already covered by a Valkey session
	// (avoids duplicate cards when a client follows the direct-play redirect).
	if h.tracker != nil && claims.IsAdmin {
		coveredPaths := make(map[string]bool, len(valkeySessions))
		coveredItems := make(map[uuid.UUID]bool, len(valkeySessions))
		for _, s := range valkeySessions {
			coveredPaths[s.FilePath] = true
			coveredItems[s.MediaItemID] = true
		}
		// A direct-play stream can surface as BOTH a file-traffic entry (path) and
		// a progress-heartbeat entry (item) at once; collapse them by client+item
		// so it shows a single card.
		emitted := make(map[string]bool)
		for _, entry := range h.tracker.List() {
			var mi *gen.SessionMediaItem
			var itemID uuid.UUID
			if entry.MediaItemID != uuid.Nil {
				// Progress-heartbeat entry: resolve by item id; it carries no path.
				if coveredItems[entry.MediaItemID] {
					continue // already shown as a Valkey (e.g. transcode) session
				}
				itemID = entry.MediaItemID
				if items, err := h.db.GetMediaItemsForSessions(ctx, []uuid.UUID{itemID}); err != nil {
					h.logger.WarnContext(ctx, "sessions: heartbeat item lookup", "err", err)
				} else if len(items) > 0 {
					mi = &items[0]
				}
				if mi == nil {
					continue // can't render a useful card without the item
				}
			} else {
				// File-traffic entry: resolve by path.
				if coveredPaths[entry.FilePath] {
					continue
				}
				m, err := h.db.GetMediaItemByFilePath(ctx, entry.FilePath)
				if err != nil && err != pgx.ErrNoRows {
					h.logger.WarnContext(ctx, "sessions: file path lookup", "err", err)
				}
				mi = m
				if mi != nil {
					itemID = mi.ID
				}
			}

			if itemID != uuid.Nil {
				dedupKey := entry.ClientIP + "|" + itemID.String()
				if emitted[dedupKey] {
					continue
				}
				emitted[dedupKey] = true
			}

			as := activeSession{
				ID:         entry.ClientIP + "|" + entry.FilePath,
				Decision:   "directPlay",
				ClientName: entry.ClientName,
				StartedAt:  entry.FirstSeen.Format("2006-01-02T15:04:05Z"),
				Title:      filepath.Base(entry.FilePath),
			}
			if itemID != uuid.Nil {
				// Stable id per client+item regardless of which entry won the dedup.
				as.ID = entry.ClientIP + "|" + itemID.String()
			}
			if mi != nil {
				as.Title = mi.Title
				as.Type = mi.Type
				posMS, durMS := h.tracker.GetItemState(mi.ID)
				as.PositionMS = posMS
				if mi.Year.Valid {
					y := int(mi.Year.Int32)
					as.Year = &y
				}
				if mi.PosterPath.Valid {
					as.PosterPath = &mi.PosterPath.String
				}
				if mi.DurationMS.Valid {
					as.DurationMS = &mi.DurationMS.Int64
				} else if durMS > 0 {
					as.DurationMS = &durMS
				}
				if mi.Bitrate.Valid {
					br := int(mi.Bitrate.Int64 / 1000) // bits/s → kbps
					as.BitrateKbps = &br
				}
			}
			resp = append(resp, as)
		}
	}

	respond.Success(w, r, resp)
}
