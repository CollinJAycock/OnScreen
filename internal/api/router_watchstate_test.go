package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/valkey"
)

// watchStateRouterDB is a WatchStateDB where every id resolves to a show.
type watchStateRouterDB struct{ marks int }

func (d *watchStateRouterDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	return gen.GetMediaItemRow{ID: id, LibraryID: uuid.New(), Type: "show", Title: "Show"}, nil
}
func (d *watchStateRouterDB) MarkWatchStateForTarget(_ context.Context, arg gen.MarkWatchStateForTargetParams) ([]uuid.UUID, error) {
	d.marks++
	return []uuid.UUID{arg.TargetID}, nil
}
func (d *watchStateRouterDB) DismissContinueWatching(context.Context, gen.DismissContinueWatchingParams) (int64, error) {
	return 1, nil
}
func (d *watchStateRouterDB) ListUpNextEpisodes(context.Context, gen.ListUpNextEpisodesParams) ([]gen.ListUpNextEpisodesRow, error) {
	return nil, nil
}

// A show/season mark fans out to one row write per episode, so the manual
// watch-state writes get their own per-user bucket well under the generic
// 1000/min session limit. Up Next is a read and stays outside it.
func TestRouter_WatchMarkRoutesHaveOwnLimiter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tm, err := auth.NewTokenMaker(auth.DeriveKey32("test-secret-key-that-is-32-bytes!"))
	if err != nil {
		t.Fatal(err)
	}
	mr := miniredis.RunT(t)
	vc, err := valkey.New(context.Background(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vc.Close() })

	db := &watchStateRouterDB{}
	router := NewRouter(&Handlers{
		Auth:        v1.NewAuthHandler(nil, logger),
		Logger:      logger,
		Auth_mw:     middleware.NewAuthenticator(tm),
		RateLimiter: valkey.NewRateLimiter(vc, logger, nil),
		WatchState:  v1.NewWatchStateHandler(db, nil, logger),
	})
	tok, err := tm.IssueAccessToken(auth.Claims{UserID: uuid.New(), Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	if watchMarkLimit.Limit >= middleware.SessionLimit.Limit {
		t.Fatalf("watchMarkLimit (%d) must be tighter than SessionLimit (%d)",
			watchMarkLimit.Limit, middleware.SessionLimit.Limit)
	}

	do := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	item := "/api/v1/items/" + uuid.New().String()

	// Mark, unmark and dismiss share the bucket.
	wantMarks := 0
	for i := 0; i < watchMarkLimit.Limit; i++ {
		method, path := http.MethodPost, item+"/watched"
		switch i % 3 {
		case 1:
			method = http.MethodDelete
		case 2:
			path = item + "/dismiss-continue-watching"
		}
		if i%3 != 2 {
			wantMarks++
		}
		rec := do(method, path)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d (%s %s) = %d, want 204: %s", i, method, path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-RateLimit-Limit"); got != strconv.Itoa(watchMarkLimit.Limit) {
			t.Fatalf("X-RateLimit-Limit = %q, want the watch-mark limit %d", got, watchMarkLimit.Limit)
		}
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, item + "/watched"},
		{http.MethodDelete, item + "/watched"},
		{http.MethodPost, item + "/dismiss-continue-watching"},
	} {
		if code := do(c.method, c.path).Code; code != http.StatusTooManyRequests {
			t.Errorf("%s %s with a spent watch-mark bucket = %d, want 429", c.method, c.path, code)
		}
	}
	if db.marks != wantMarks {
		t.Errorf("marks written = %d, want %d (none once limited)", db.marks, wantMarks)
	}
	if code := do(http.MethodGet, item+"/up-next").Code; code != http.StatusOK {
		t.Errorf("up-next with a spent watch-mark bucket = %d, want 200 (reads are not in it)", code)
	}
}
