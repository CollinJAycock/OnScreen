package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/api/respond"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/userstats"
)

// UserStatsService computes a user's own stats. Satisfied by
// *userstats.Service.
type UserStatsService interface {
	Compute(ctx context.Context, v userstats.Viewer, r userstats.Range, tz string, loc *time.Location) (*userstats.Stats, error)
	YearInReview(ctx context.Context, v userstats.Viewer, year int, now time.Time, tz string, loc *time.Location) (*userstats.YearInReview, error)
}

// UserStatsHandler serves the caller's own stats (My stats) and Year in
// review. Only ever the caller's: there is no user parameter, and a managed
// profile signed in as itself sees the profile's plays. Admins already have
// the server-wide Analytics dashboard, so there is no admin view of another
// user's numbers.
type UserStatsHandler struct {
	svc    UserStatsService
	access LibraryAccessChecker
	logger *slog.Logger
	now    func() time.Time
}

// NewUserStatsHandler creates a UserStatsHandler.
func NewUserStatsHandler(svc UserStatsService, logger *slog.Logger) *UserStatsHandler {
	return &UserStatsHandler{svc: svc, logger: logger, now: time.Now}
}

// WithLibraryAccess limits the stats to the caller's libraries.
func (h *UserStatsHandler) WithLibraryAccess(a LibraryAccessChecker) *UserStatsHandler {
	h.access = a
	return h
}

// LibraryAccessWired reports whether the library-ACL checker is wired (a nil
// checker fails open; see api.Handlers.ValidateLibraryAccess).
func (h *UserStatsHandler) LibraryAccessWired() bool { return h.access != nil }

// viewer builds the stats viewer for claims: the caller's libraries, rating
// ceiling and label restriction, as every item listing applies them.
func (h *UserStatsHandler) viewer(ctx context.Context, claims *auth.Claims) (userstats.Viewer, error) {
	v := userstats.Viewer{
		UserID:           claims.UserID,
		MaxContentRating: claims.MaxContentRating,
		Labels:           claims.LabelFilter(),
	}
	if h.access != nil {
		allowed, err := h.access.AllowedLibraryIDs(ctx, claims.UserID, claims.IsAdmin)
		if err != nil {
			return v, err
		}
		v.LibraryIDs = userstats.LibraryIDList(allowed)
	}
	return v, nil
}

// Stats handles GET /api/v1/users/me/stats?range=30d|90d|365d|all|year:YYYY&tz=<IANA zone>.
// Days, months and the weekday/hour heatmap bucket in tz (default UTC).
func (h *UserStatsHandler) Stats(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	tz := displayTZ(r.URL.Query().Get("tz"))
	loc, _ := time.LoadLocation(tz)
	rng, err := userstats.ParseRange(r.URL.Query().Get("range"), h.now(), loc)
	if err != nil {
		respond.BadRequest(w, r, "range must be 30d, 90d, 365d, all or year:YYYY")
		return
	}
	v, err := h.viewer(r.Context(), claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "user stats: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	out, err := h.svc.Compute(r.Context(), v, rng, tz, loc)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "user stats: compute", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, out)
}

// YearInReview handles GET /api/v1/users/me/year-in-review?year=YYYY&tz=<IANA zone>.
// year defaults to the current year in tz.
func (h *UserStatsHandler) YearInReview(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	if claims == nil {
		respond.Unauthorized(w, r)
		return
	}
	tz := displayTZ(r.URL.Query().Get("tz"))
	loc, _ := time.LoadLocation(tz)
	now := h.now()
	year := now.In(loc).Year()
	if raw := r.URL.Query().Get("year"); raw != "" {
		y, err := strconv.Atoi(raw)
		if err != nil || len(raw) != 4 {
			respond.BadRequest(w, r, "year must be a four-digit year")
			return
		}
		year = y
	}
	v, err := h.viewer(r.Context(), claims)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "year in review: allowed libraries", "err", err)
		respond.InternalError(w, r)
		return
	}
	out, err := h.svc.YearInReview(r.Context(), v, year, now, tz, loc)
	if errors.Is(err, userstats.ErrBadRange) {
		respond.BadRequest(w, r, "year must be between 2000 and the current year")
		return
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "year in review: compute", "err", err)
		respond.InternalError(w, r)
		return
	}
	respond.Success(w, r, out)
}
