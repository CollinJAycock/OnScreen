package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/userstats"
)

type fakeUserStats struct {
	viewer userstats.Viewer
	rng    userstats.Range
	tz     string
	year   int
	err    error
}

func (f *fakeUserStats) Compute(_ context.Context, v userstats.Viewer, r userstats.Range, tz string, _ *time.Location) (*userstats.Stats, error) {
	f.viewer, f.rng, f.tz = v, r, tz
	if f.err != nil {
		return nil, f.err
	}
	return &userstats.Stats{Range: r.Key}, nil
}

func (f *fakeUserStats) YearInReview(_ context.Context, v userstats.Viewer, year int, now time.Time, tz string, loc *time.Location) (*userstats.YearInReview, error) {
	f.viewer, f.year, f.tz = v, year, tz
	if _, err := userstats.YearRange(year, now, loc); err != nil {
		return nil, err
	}
	if f.err != nil {
		return nil, f.err
	}
	return &userstats.YearInReview{Year: year}, nil
}

type statsAccess struct{ libs map[uuid.UUID]struct{} }

func (a statsAccess) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return true, nil
}
func (a statsAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	return a.libs, nil
}

func statsRequest(target string, claims *auth.Claims) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	if claims != nil {
		r = r.WithContext(middleware.WithClaims(r.Context(), claims))
	}
	return r
}

func TestUserStats_AppliesCallerVisibility(t *testing.T) {
	lib := uuid.New()
	svc := &fakeUserStats{}
	h := NewUserStatsHandler(svc, slog.Default()).WithLibraryAccess(statsAccess{libs: map[uuid.UUID]struct{}{lib: {}}})
	claims := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG", BlockedLabels: []string{"horror"}}

	rec := httptest.NewRecorder()
	h.Stats(rec, statsRequest("/api/v1/users/me/stats?range=90d&tz=America/Detroit", claims))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	v := svc.viewer
	if v.UserID != claims.UserID || v.MaxContentRating != "PG" || len(v.Labels.Blocked) != 1 {
		t.Errorf("viewer = %+v", v)
	}
	if len(v.LibraryIDs) != 1 || v.LibraryIDs[0] != lib {
		t.Errorf("library ids = %v", v.LibraryIDs)
	}
	if svc.rng.Key != "90d" || svc.tz != "America/Detroit" {
		t.Errorf("range %q tz %q", svc.rng.Key, svc.tz)
	}

	// Admin: every library (nil), not an empty set.
	h.Stats(httptest.NewRecorder(), statsRequest("/api/v1/users/me/stats", &auth.Claims{UserID: uuid.New(), IsAdmin: true}))
	if svc.viewer.LibraryIDs != nil || svc.rng.Key != "30d" || svc.tz != "UTC" {
		t.Errorf("admin viewer libs %v range %q tz %q", svc.viewer.LibraryIDs, svc.rng.Key, svc.tz)
	}

	// A user with no grants gets an empty (non-nil) set: nothing, not everything.
	h = NewUserStatsHandler(svc, slog.Default()).WithLibraryAccess(statsAccess{libs: map[uuid.UUID]struct{}{}})
	h.Stats(httptest.NewRecorder(), statsRequest("/api/v1/users/me/stats", &auth.Claims{UserID: uuid.New()}))
	if svc.viewer.LibraryIDs == nil {
		t.Error("no grants must not widen to every library")
	}
}

func TestUserStats_Errors(t *testing.T) {
	svc := &fakeUserStats{}
	h := NewUserStatsHandler(svc, slog.Default()).WithLibraryAccess(statsAccess{})
	claims := &auth.Claims{UserID: uuid.New()}

	cases := []struct {
		name   string
		call   func(http.ResponseWriter, *http.Request)
		target string
		claims *auth.Claims
		want   int
	}{
		{"stats unauthenticated", h.Stats, "/api/v1/users/me/stats", nil, http.StatusUnauthorized},
		{"bad range", h.Stats, "/api/v1/users/me/stats?range=7d", claims, http.StatusBadRequest},
		{"review unauthenticated", h.YearInReview, "/api/v1/users/me/year-in-review", nil, http.StatusUnauthorized},
		{"review bad year", h.YearInReview, "/api/v1/users/me/year-in-review?year=20x6", claims, http.StatusBadRequest},
		{"review future year", h.YearInReview, "/api/v1/users/me/year-in-review?year=2999", claims, http.StatusBadRequest},
		{"review ok", h.YearInReview, "/api/v1/users/me/year-in-review?year=2025", claims, http.StatusOK},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		tc.call(rec, statsRequest(tc.target, tc.claims))
		if rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	if svc.year != 2025 {
		t.Errorf("year = %d", svc.year)
	}

	svc.err = errors.New("db down")
	rec := httptest.NewRecorder()
	h.Stats(rec, statsRequest("/api/v1/users/me/stats", claims))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("db error status = %d", rec.Code)
	}
}
