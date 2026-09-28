package uat

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// ── Manual watch state — any signed-in user, never anonymous ─────────────────

// stubWatchStateDB satisfies v1.WatchStateDB and v1.LibraryWatchDB.
type stubWatchStateDB struct {
	items map[uuid.UUID]gen.GetMediaItemRow
}

func (s *stubWatchStateDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := s.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}
func (s *stubWatchStateDB) MarkWatchStateForTarget(context.Context, gen.MarkWatchStateForTargetParams) (int64, error) {
	return 1, nil
}
func (s *stubWatchStateDB) DismissContinueWatching(context.Context, gen.DismissContinueWatchingParams) (int64, error) {
	return 1, nil
}
func (s *stubWatchStateDB) ListUpNextEpisodes(context.Context, gen.ListUpNextEpisodesParams) ([]gen.ListUpNextEpisodesRow, error) {
	return nil, nil
}
func (s *stubWatchStateDB) ListItemWatchSummaries(context.Context, gen.ListItemWatchSummariesParams) ([]gen.ListItemWatchSummariesRow, error) {
	return nil, nil
}
func (s *stubWatchStateDB) PickRandomLibraryItem(_ context.Context, arg gen.PickRandomLibraryItemParams) (gen.PickRandomLibraryItemRow, error) {
	for _, it := range s.items {
		if it.LibraryID == arg.LibraryID && it.Type == arg.ItemType {
			return gen.PickRandomLibraryItemRow{ID: it.ID, Type: it.Type}, nil
		}
	}
	return gen.PickRandomLibraryItemRow{}, pgx.ErrNoRows
}

type watchFixture struct {
	lib, movie, show uuid.UUID
}

func newWatchStateServer(t *testing.T) (*testServer, watchFixture) {
	fx := watchFixture{lib: uuid.New(), movie: uuid.New(), show: uuid.New()}
	db := &stubWatchStateDB{items: map[uuid.UUID]gen.GetMediaItemRow{
		fx.movie: {ID: fx.movie, LibraryID: fx.lib, Type: "movie", Title: "M"},
		fx.show:  {ID: fx.show, LibraryID: fx.lib, Type: "show", Title: "S"},
	}}
	libSvc := newStubLibraryService()
	libSvc.libs[fx.lib] = &library.Library{ID: fx.lib, Name: "Movies", Type: "movie", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.WatchState = v1.NewWatchStateHandler(db, stubUpcomingAccess{}, slog.Default())
		h.Library = v1.NewLibraryHandler(libSvc, slog.Default()).
			WithMedia(&stubMediaItemLister{}).
			WithWatchState(db)
	})
	return ts, fx
}

func TestWatchState_RoutesRequireAuth(t *testing.T) {
	ts, fx := newWatchStateServer(t)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/items/" + fx.movie.String() + "/watched"},
		{"DELETE", "/api/v1/items/" + fx.movie.String() + "/watched"},
		{"POST", "/api/v1/items/" + fx.movie.String() + "/dismiss-continue-watching"},
		{"GET", "/api/v1/items/" + fx.show.String() + "/up-next"},
		{"GET", "/api/v1/libraries/" + fx.lib.String() + "/random"},
	} {
		resp := ts.do(tc.method, tc.path, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without token: status = %d, want 401", tc.method, tc.path, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// Marking, dismissing and Up Next are per-user self-service: a regular
// (non-admin) account gets them, scoped to itself.
func TestWatchState_RoutesOpenToNonAdmin(t *testing.T) {
	ts, fx := newWatchStateServer(t)
	tok := ts.userToken()
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/v1/items/" + fx.movie.String() + "/watched", http.StatusNoContent},
		{"DELETE", "/api/v1/items/" + fx.movie.String() + "/watched", http.StatusNoContent},
		{"POST", "/api/v1/items/" + fx.show.String() + "/watched", http.StatusNoContent},
		{"POST", "/api/v1/items/" + fx.movie.String() + "/dismiss-continue-watching", http.StatusNoContent},
		{"GET", "/api/v1/items/" + fx.show.String() + "/up-next", http.StatusOK},
		{"GET", "/api/v1/items/" + fx.movie.String() + "/up-next", http.StatusUnprocessableEntity},
		{"POST", "/api/v1/items/" + uuid.New().String() + "/watched", http.StatusNotFound},
		{"GET", "/api/v1/libraries/" + fx.lib.String() + "/random", http.StatusOK},
		{"GET", "/api/v1/libraries/" + fx.lib.String() + "/random?watch=bogus", http.StatusBadRequest},
		{"GET", "/api/v1/libraries/" + fx.lib.String() + "/items?watch=bogus", http.StatusBadRequest},
	} {
		resp := ts.do(tc.method, tc.path, tok, nil)
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s: status = %d, want %d (body=%s)", tc.method, tc.path, resp.StatusCode, tc.want, readBody(resp))
		}
		resp.Body.Close()
	}
}

func TestWatchState_UpNextShape(t *testing.T) {
	ts, fx := newWatchStateServer(t)
	resp := ts.do("GET", "/api/v1/items/"+fx.show.String()+"/up-next", ts.userToken(), nil)
	assertStatus(t, resp, http.StatusOK)
	var body struct {
		Data map[string]any `json:"data"`
	}
	mustDecode(t, resp, &body)
	if body.Data["mode"] != "none" {
		t.Errorf("mode = %v, want none (show with no episodes)", body.Data["mode"])
	}
	if _, ok := body.Data["episode"]; ok {
		t.Error("episode must be omitted for mode none")
	}
}
