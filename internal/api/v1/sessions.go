package v1

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/streaming"
	"github.com/onscreen/onscreen/internal/transcode"
)

// sessionActivityTimeout is how long a transcode session can be inactive
// (no progress heartbeat) before being hidden from "Now Playing".
const sessionActivityTimeout = 2 * time.Minute

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
}

// List handles GET /api/v1/sessions.
func (h *NativeSessionsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
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
	if h.tracker != nil {
		coveredPaths := make(map[string]bool, len(valkeySessions))
		for _, s := range valkeySessions {
			coveredPaths[s.FilePath] = true
		}
		for _, entry := range h.tracker.List() {
			if coveredPaths[entry.FilePath] {
				continue
			}
			as := activeSession{
				ID:         entry.ClientIP + "|" + entry.FilePath,
				Decision:   "directPlay",
				ClientName: entry.ClientName,
				StartedAt:  entry.FirstSeen.Format("2006-01-02T15:04:05Z"),
				Title:      filepath.Base(entry.FilePath),
			}
			mi, err := h.db.GetMediaItemByFilePath(ctx, entry.FilePath)
			if err != nil && err != pgx.ErrNoRows {
				h.logger.WarnContext(ctx, "sessions: file path lookup", "err", err)
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
