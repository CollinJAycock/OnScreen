package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/domain/watchlimit"
)

// WatchLimitStore is the per-user parental-policy surface the handler needs.
// Satisfied by *watchlimit.Store.
type WatchLimitStore interface {
	GetPolicy(ctx context.Context, userID uuid.UUID) (watchlimit.Policy, error)
	SetPolicy(ctx context.Context, userID uuid.UUID, p watchlimit.Policy) error
	TodayUsageSeconds(ctx context.Context, userID uuid.UUID, day time.Time) (int, error)
}

// WatchLimitHandler serves the parental watch-limit endpoints: an admin setter
// and a self read that also reports today's usage + whether playback is
// currently allowed (so clients can show "45 min left" / "allowed until 8 PM").
type WatchLimitHandler struct {
	store WatchLimitStore
}

// NewWatchLimitHandler constructs a WatchLimitHandler.
func NewWatchLimitHandler(store WatchLimitStore) *WatchLimitHandler {
	return &WatchLimitHandler{store: store}
}

type watchLimitResponse struct {
	DailyLimitMinutes  *int   `json:"daily_limit_minutes"`
	AllowedStartMinute *int   `json:"allowed_start_minute"`
	AllowedEndMinute   *int   `json:"allowed_end_minute"`
	UsedMinutesToday   int    `json:"used_minutes_today"`
	RemainingMinutes   *int   `json:"remaining_minutes,omitempty"`
	Allowed            bool   `json:"allowed"`
	Reason             string `json:"reason,omitempty"`
}

// Get handles GET /api/v1/users/me/watch-limit — the caller's own policy plus
// today's usage and current allowed/blocked state.
func (h *WatchLimitHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	h.respondPolicy(w, r, claims.UserID)
}

// GetForUser handles GET /api/v1/users/{id}/watch-limit (admin only) — a target
// user's policy plus today's usage, so the admin UI can pre-fill the editor
// with the current values before a Set. Mirrors Set's admin gating.
func (h *WatchLimitHandler) GetForUser(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	h.respondPolicy(w, r, targetID)
}

// respondPolicy loads userID's policy + today's usage, evaluates the current
// allowed/blocked state, and writes the watchLimitResponse. Shared by the self
// Get and the admin GetForUser.
func (h *WatchLimitHandler) respondPolicy(w http.ResponseWriter, r *http.Request, userID uuid.UUID) {
	policy, err := h.store.GetPolicy(r.Context(), userID)
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	now := time.Now()
	used, err := h.store.TodayUsageSeconds(r.Context(), userID, watchlimit.LocalDay(now))
	if err != nil {
		respond.InternalError(w, r)
		return
	}
	allowed, reason := watchlimit.Evaluate(policy, used, now)
	resp := watchLimitResponse{
		DailyLimitMinutes:  policy.DailyLimitMinutes,
		AllowedStartMinute: policy.AllowedStartMinute,
		AllowedEndMinute:   policy.AllowedEndMinute,
		UsedMinutesToday:   used / 60,
		Allowed:            allowed,
		Reason:             reason,
	}
	if policy.DailyLimitMinutes != nil {
		rem := *policy.DailyLimitMinutes - used/60
		if rem < 0 {
			rem = 0
		}
		resp.RemainingMinutes = &rem
	}
	respond.Success(w, r, resp)
}

// Set handles PUT /api/v1/users/{id}/watch-limit (admin only). Each field is a
// pointer so omitting one clears that limit; the two window bounds must be set
// or cleared together. Mirrors SetContentRating's admin-gated shape.
func (h *WatchLimitHandler) Set(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil || !claims.IsAdmin {
		respond.Forbidden(w, r)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respond.BadRequest(w, r, "invalid user id")
		return
	}
	var body struct {
		DailyLimitMinutes  *int `json:"daily_limit_minutes"`
		AllowedStartMinute *int `json:"allowed_start_minute"`
		AllowedEndMinute   *int `json:"allowed_end_minute"`
	}
	// Reject unknown fields so a client typo (e.g. daily_limit instead of
	// daily_limit_minutes) fails loudly instead of silently clearing the cap.
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		respond.BadRequest(w, r, "invalid request body: "+err.Error())
		return
	}
	if body.DailyLimitMinutes != nil && *body.DailyLimitMinutes < 0 {
		respond.BadRequest(w, r, "daily_limit_minutes must be >= 0")
		return
	}
	for _, m := range []*int{body.AllowedStartMinute, body.AllowedEndMinute} {
		if m != nil && (*m < 0 || *m >= 1440) {
			respond.BadRequest(w, r, "allowed_start_minute / allowed_end_minute must be in [0,1440)")
			return
		}
	}
	if (body.AllowedStartMinute == nil) != (body.AllowedEndMinute == nil) {
		respond.BadRequest(w, r, "allowed_start_minute and allowed_end_minute must be set together")
		return
	}
	if err := h.store.SetPolicy(r.Context(), targetID, watchlimit.Policy{
		DailyLimitMinutes:  body.DailyLimitMinutes,
		AllowedStartMinute: body.AllowedStartMinute,
		AllowedEndMinute:   body.AllowedEndMinute,
	}); err != nil {
		respond.InternalError(w, r)
		return
	}
	respond.NoContent(w)
}
