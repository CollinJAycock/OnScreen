package v1

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/notification"
)

// PlaybackHandler powers the cross-device "play on this device" remote
// control. Two endpoints:
//
//   - GET  /api/v1/playback/devices  → list distinct client_name values
//     the user has scrobbled from in the last 30 days, with the most
//     recent first. Drives the "Play on…" picker UI.
//
//   - POST /api/v1/playback/transfer → broadcast a `playback.transfer`
//     event to the user's SSE channel. Each connected client filters
//     by `target_client_name` against its own client name and starts
//     playback on a match. The server stays oblivious to which device
//     accepts — there's no per-device routing on the broker, just the
//     existing per-user fan-out, so the matching policy lives on the
//     receiving side. Same model the existing `progress.updated` path
//     uses; keeps the broker minimal.
//
// Auth: both endpoints sit inside the same authenticated group as
// /items/{id}/progress and rely on the user owning the item. The
// transfer publish doesn't validate library access on the target item
// itself — the receiving client re-fetches /items/{id} which goes
// through the standard library-access check, so a malicious transfer
// pointing at an item the user can't see fails on the receiver, not
// here.
type PlaybackHandler struct {
	queries *gen.Queries
	sync    *notification.Broker
	logger  *slog.Logger
}

func NewPlaybackHandler(queries *gen.Queries, sync *notification.Broker, logger *slog.Logger) *PlaybackHandler {
	return &PlaybackHandler{queries: queries, sync: sync, logger: logger}
}

// DeviceRow is one entry in the device picker. last_seen is informational —
// the UI sorts by recency and may surface "last used 3 hours ago" labels.
type DeviceRow struct {
	ClientName string    `json:"client_name"`
	LastSeen   time.Time `json:"last_seen"`
}

// Devices handles GET /api/v1/playback/devices.
func (h *PlaybackHandler) Devices(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	rows, err := h.queries.ListRecentClientNamesForUser(r.Context(), claims.UserID)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list devices", "err", err)
		respond.InternalError(w, r)
		return
	}
	out := make([]DeviceRow, 0, len(rows))
	for _, row := range rows {
		// ClientName is queried with a non-null filter so the
		// pointer is non-nil in normal flow — defensive read so a
		// future schema change can't break the response shape.
		if row.ClientName == nil {
			continue
		}
		// last_seen comes back as pgtype.Timestamptz; valid is true
		// for any row the GROUP BY produced (it's the MAX of a
		// non-null column).
		if !row.LastSeen.Valid {
			continue
		}
		out = append(out, DeviceRow{
			ClientName: *row.ClientName,
			LastSeen:   row.LastSeen.Time,
		})
	}
	respond.Success(w, r, out)
}

// TransferRequest is the POST body for `/playback/transfer`.
//
// `target_client_name` is matched literally against each receiving
// client's own `client_name` value. The web client uses a stable
// `<browser>` label; native clients use device-derived names ("Pixel 8",
// "Living Room TV", etc.). Empty `target_client_name` is rejected —
// broadcast-to-all-devices isn't supported, since any device receiving
// the transfer would pause whatever else is playing on it.
type TransferRequest struct {
	ItemID           string `json:"item_id"`
	PositionMS       int64  `json:"position_ms"`
	TargetClientName string `json:"target_client_name"`
}

// TransferPayload is the SSE-event `data` blob each receiving client
// parses. `started_by_client` lets the receiver suppress the event
// when it would echo back to the originator (a phone that initiated
// the transfer shouldn't also receive + react to it).
type TransferPayload struct {
	ItemID           string `json:"item_id"`
	PositionMS       int64  `json:"position_ms"`
	TargetClientName string `json:"target_client_name"`
}

// Transfer handles POST /api/v1/playback/transfer.
func (h *PlaybackHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	var body TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid body")
		return
	}
	if body.ItemID == "" {
		respond.BadRequest(w, r, "item_id required")
		return
	}
	if _, err := uuid.Parse(body.ItemID); err != nil {
		respond.BadRequest(w, r, "item_id must be a UUID")
		return
	}
	if body.TargetClientName == "" {
		respond.BadRequest(w, r, "target_client_name required")
		return
	}
	if body.PositionMS < 0 {
		body.PositionMS = 0
	}
	if h.sync == nil {
		// Broker not configured — surface that explicitly so the
		// caller knows the transfer didn't actually fire (vs. silently
		// dropping which would look like the target device just
		// didn't respond).
		respond.InternalError(w, r)
		return
	}
	data, err := json.Marshal(TransferPayload{
		ItemID:           body.ItemID,
		PositionMS:       body.PositionMS,
		TargetClientName: body.TargetClientName,
	})
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	itemIDStr := body.ItemID
	h.sync.Publish(claims.UserID, notification.Event{
		Type:      "playback.transfer",
		ItemID:    &itemIDStr,
		CreatedAt: time.Now().UnixMilli(),
		Data:      data,
	})
	respond.NoContent(w)
}
