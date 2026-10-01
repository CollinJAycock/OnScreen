package v1

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
)

// Discover is the TMDB catalogue behind the request flow. A server without a
// TMDB key answers the feature-off 503 (never a 500), and an account that
// may not request is refused before TMDB is asked anything.

type gateFakeTMDB struct {
	err   error
	calls int
}

func (f *gateFakeTMDB) SearchMulti(context.Context, string, int) ([]tmdb.DiscoverResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return []tmdb.DiscoverResult{{MediaType: "movie", TMDBID: 1, Title: "Sintel", Year: 2010}}, nil
}

type gateFakeDiscoverDB struct{}

func (gateFakeDiscoverDB) ListMediaItemsByTMDBIDs(context.Context, gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	return nil, nil
}

type fakeRequestGate struct {
	allowed bool
	err     error
	calls   int
}

func (f *fakeRequestGate) CanRequest(context.Context, uuid.UUID) (bool, error) {
	f.calls++
	return f.allowed, f.err
}

func discoverSearch(h *DiscoverHandler, claims *auth.Claims) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/discover/search?q=sintel", nil)
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	return rec
}

func TestDiscoverSearch_NoTMDBKeyIsFeatureOff(t *testing.T) {
	user := &auth.Claims{UserID: uuid.New()}
	cases := []struct {
		name string
		tmdb DiscoverTMDB
	}{
		{"no agent wired", nil},
		// main.go's adapter: the agent func returns nil without a key.
		{"adapter reports no agent", &gateFakeTMDB{err: fmt.Errorf("requests: tmdb agent not configured: %w", ErrDiscoverTMDBUnavailable)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := NewDiscoverHandler(gateFakeDiscoverDB{}, c.tmdb, nil, slog.Default())
			rec := discoverSearch(h, user)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (%s)", rec.Code, rec.Body)
			}
			if code, _ := errorCode(t, rec); code != "TMDB_UNAVAILABLE" {
				t.Errorf("code = %q, want TMDB_UNAVAILABLE", code)
			}
		})
	}

	// Any other TMDB failure is still an error, not "not configured".
	h := NewDiscoverHandler(gateFakeDiscoverDB{}, &gateFakeTMDB{err: errors.New("tmdb 500")}, nil, slog.Default())
	if rec := discoverSearch(h, user); rec.Code != http.StatusInternalServerError {
		t.Errorf("tmdb failure: status = %d, want 500", rec.Code)
	}
}

func TestDiscoverSearch_RequestGate(t *testing.T) {
	user := &auth.Claims{UserID: uuid.New()}
	admin := &auth.Claims{UserID: uuid.New(), IsAdmin: true}

	t.Run("requesting off is refused before TMDB", func(t *testing.T) {
		tm, gate := &gateFakeTMDB{}, &fakeRequestGate{allowed: false}
		h := NewDiscoverHandler(gateFakeDiscoverDB{}, tm, nil, slog.Default()).WithRequestGate(gate)
		rec := discoverSearch(h, user)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body)
		}
		if code, _ := errorCode(t, rec); code != "REQUESTS_DISABLED" {
			t.Errorf("code = %q, want REQUESTS_DISABLED (what POST /requests answers)", code)
		}
		if tm.calls != 0 {
			t.Errorf("TMDB searched %d times for an account that can't request", tm.calls)
		}
	})

	t.Run("lookup failure fails closed", func(t *testing.T) {
		tm, gate := &gateFakeTMDB{}, &fakeRequestGate{err: errors.New("db down")}
		h := NewDiscoverHandler(gateFakeDiscoverDB{}, tm, nil, slog.Default()).WithRequestGate(gate)
		if rec := discoverSearch(h, user); rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
		if tm.calls != 0 {
			t.Error("TMDB searched although the can-request lookup failed")
		}
	})

	t.Run("allowed user searches", func(t *testing.T) {
		tm, gate := &gateFakeTMDB{}, &fakeRequestGate{allowed: true}
		h := NewDiscoverHandler(gateFakeDiscoverDB{}, tm, nil, slog.Default()).WithRequestGate(gate)
		if rec := discoverSearch(h, user); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if tm.calls != 1 {
			t.Errorf("TMDB calls = %d, want 1", tm.calls)
		}
	})

	t.Run("admins skip the lookup", func(t *testing.T) {
		tm, gate := &gateFakeTMDB{}, &fakeRequestGate{allowed: false}
		h := NewDiscoverHandler(gateFakeDiscoverDB{}, tm, nil, slog.Default()).WithRequestGate(gate)
		if rec := discoverSearch(h, admin); rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 (%s)", rec.Code, rec.Body)
		}
		if gate.calls != 0 {
			t.Errorf("gate consulted %d times for an admin", gate.calls)
		}
	})

	// With no TMDB wired at all the handler answers feature-off before the
	// lookup. (In production TMDB is the agent adapter, which only reports
	// "no agent" when called, so a refused account gets the 403 first —
	// either way a clean refusal, never a 500.)
	t.Run("no TMDB wired answers before the gate", func(t *testing.T) {
		gate := &fakeRequestGate{allowed: false}
		h := NewDiscoverHandler(gateFakeDiscoverDB{}, nil, nil, slog.Default()).WithRequestGate(gate)
		if rec := discoverSearch(h, user); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
	})
}

func TestDiscoverSeasons_RequestGate(t *testing.T) {
	uid := uuid.New()
	seasons, gate := &fakeSeasonTMDB{}, &fakeRequestGate{allowed: false}
	h := NewDiscoverHandler(nil, nil, nil, slog.Default()).
		WithRequestGate(gate).
		WithSeasons(seasons, seasonDBFixture())
	rec := httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid}))
	if code, _ := errorCode(t, rec); rec.Code != http.StatusForbidden || code != "REQUESTS_DISABLED" {
		t.Errorf("status = %d (%s), want 403 REQUESTS_DISABLED", rec.Code, rec.Body)
	}
	if seasons.calls != 0 {
		t.Errorf("TMDB season list read %d times for an account that can't request", seasons.calls)
	}

	gate.allowed = true
	rec = httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid}))
	if rec.Code != http.StatusOK {
		t.Errorf("allowed: status = %d, want 200 (%s)", rec.Code, rec.Body)
	}
}
