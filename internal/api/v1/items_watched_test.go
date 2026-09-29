package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
)

// ── fake store ───────────────────────────────────────────────────────────────

type fakeWatchStateDB struct {
	items map[uuid.UUID]gen.GetMediaItemRow

	markCalls    []gen.MarkWatchStateForTargetParams
	marked       []uuid.UUID // what a mark reports it changed
	dismissCalls []gen.DismissContinueWatchingParams
	dismissRows  int64
	upNextCalls  []gen.ListUpNextEpisodesParams
	upNextRows   []gen.ListUpNextEpisodesRow
	err          error
}

func (f *fakeWatchStateDB) GetMediaItem(_ context.Context, id uuid.UUID) (gen.GetMediaItemRow, error) {
	it, ok := f.items[id]
	if !ok {
		return gen.GetMediaItemRow{}, pgx.ErrNoRows
	}
	return it, nil
}
func (f *fakeWatchStateDB) MarkWatchStateForTarget(_ context.Context, arg gen.MarkWatchStateForTargetParams) ([]uuid.UUID, error) {
	f.markCalls = append(f.markCalls, arg)
	return f.marked, f.err
}
func (f *fakeWatchStateDB) DismissContinueWatching(_ context.Context, arg gen.DismissContinueWatchingParams) (int64, error) {
	f.dismissCalls = append(f.dismissCalls, arg)
	return f.dismissRows, f.err
}
func (f *fakeWatchStateDB) ListUpNextEpisodes(_ context.Context, arg gen.ListUpNextEpisodesParams) ([]gen.ListUpNextEpisodesRow, error) {
	f.upNextCalls = append(f.upNextCalls, arg)
	return f.upNextRows, f.err
}

func (f *fakeWatchStateDB) add(itemType, rating string, lib uuid.UUID) uuid.UUID {
	id := uuid.New()
	var cr *string
	if rating != "" {
		cr = &rating
	}
	if f.items == nil {
		f.items = map[uuid.UUID]gen.GetMediaItemRow{}
	}
	f.items[id] = gen.GetMediaItemRow{ID: id, LibraryID: lib, Type: itemType, Title: itemType, ContentRating: cr}
	return id
}

func watchStateRequest(method string, id uuid.UUID, claims *auth.Claims) *http.Request {
	req := withChiParam(httptest.NewRequest(method, "/", nil), "id", id.String())
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	return req
}

// ── mark / unmark ────────────────────────────────────────────────────────────

func TestWatchState_Mark_TargetsAndState(t *testing.T) {
	lib := uuid.New()
	db := &fakeWatchStateDB{}
	access := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{lib: {}}}
	h := NewWatchStateHandler(db, access, slog.Default())
	user := uuid.New()
	claims := &auth.Claims{UserID: user, MaxContentRating: "PG-13"}

	for _, tc := range []struct {
		typ   string
		call  func(http.ResponseWriter, *http.Request)
		state string
	}{
		{"movie", h.MarkWatched, "watched"},
		{"episode", h.MarkUnwatched, "unwatched"},
		{"season", h.MarkWatched, "watched"},
		{"show", h.MarkUnwatched, "unwatched"},
		{"home_video", h.MarkWatched, "watched"},
	} {
		id := db.add(tc.typ, "PG", lib)
		rec := httptest.NewRecorder()
		tc.call(rec, watchStateRequest(http.MethodPost, id, claims))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("%s: status %d body=%s", tc.typ, rec.Code, rec.Body.String())
		}
		got := db.markCalls[len(db.markCalls)-1]
		if got.TargetID != id || got.TargetType != tc.typ || got.State != tc.state || got.UserID != user {
			t.Errorf("%s: mark params = %+v", tc.typ, got)
		}
		// The ceiling travels with the call so a show expands only to
		// episodes the caller can see.
		if got.MaxRatingRank == nil || *got.MaxRatingRank != 2 {
			t.Errorf("%s: max rating rank = %v, want 2 (PG-13)", tc.typ, got.MaxRatingRank)
		}
	}
}

func TestWatchState_Mark_RejectsNonVideo(t *testing.T) {
	db := &fakeWatchStateDB{}
	h := NewWatchStateHandler(db, nil, slog.Default())
	for _, typ := range []string{"track", "photo", "album", "audiobook", "book"} {
		id := db.add(typ, "G", uuid.New())
		rec := httptest.NewRecorder()
		h.MarkWatched(rec, watchStateRequest(http.MethodPost, id, &auth.Claims{UserID: uuid.New()}))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d, want 422", typ, rec.Code)
		}
	}
	if len(db.markCalls) != 0 {
		t.Errorf("no mark may be written for non-video types, got %d", len(db.markCalls))
	}
}

// The gate is the one every item surface applies: an item in a library the
// caller can't see, or above their rating ceiling, is indistinguishable from
// a missing one.
func TestWatchState_Gate(t *testing.T) {
	hidden := uuid.New()
	visible := uuid.New()
	db := &fakeWatchStateDB{}
	access := &stubLibraryAccessChecker{allowed: map[uuid.UUID]struct{}{visible: {}}}
	h := NewWatchStateHandler(db, access, slog.Default())
	kid := &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG"}

	inHidden := db.add("movie", "G", hidden)
	overCeiling := db.add("movie", "R", visible)
	unrated := db.add("movie", "", visible)

	calls := map[string]func(http.ResponseWriter, *http.Request){
		"mark":    h.MarkWatched,
		"unmark":  h.MarkUnwatched,
		"dismiss": h.DismissContinueWatching,
		"up-next": h.UpNext,
	}
	for name, call := range calls {
		for _, id := range []uuid.UUID{inHidden, overCeiling, unrated, uuid.New()} {
			rec := httptest.NewRecorder()
			call(rec, watchStateRequest(http.MethodPost, id, kid))
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: status %d, want 404", name, id, rec.Code)
			}
		}
	}
	if len(db.markCalls)+len(db.dismissCalls)+len(db.upNextCalls) != 0 {
		t.Error("gated requests must not reach the store")
	}

	// No claims → 401.
	rec := httptest.NewRecorder()
	h.MarkWatched(rec, watchStateRequest(http.MethodPost, inHidden, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims: status %d, want 401", rec.Code)
	}
	// Malformed id → 400.
	rec = httptest.NewRecorder()
	req := withChiParam(httptest.NewRequest(http.MethodPost, "/", nil), "id", "nope")
	req = req.WithContext(middleware.WithClaims(req.Context(), kid))
	h.MarkWatched(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: status %d, want 400", rec.Code)
	}
}

func TestWatchState_Mark_StoreErrorIs500(t *testing.T) {
	db := &fakeWatchStateDB{err: errors.New("boom")}
	h := NewWatchStateHandler(db, nil, slog.Default())
	id := db.add("movie", "G", uuid.New())
	rec := httptest.NewRecorder()
	h.MarkWatched(rec, watchStateRequest(http.MethodPost, id, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}

// A "watched" mark that changed something reports what it changed to the
// hook (the Trakt history add); unwatched marks, marks that changed nothing
// and failed marks don't.
func TestWatchState_Mark_TellsTheWatchedHook(t *testing.T) {
	type heard struct {
		user uuid.UUID
		ids  []uuid.UUID
	}
	episodes := []uuid.UUID{uuid.New(), uuid.New()}
	for _, tc := range []struct {
		name    string
		unmark  bool
		marked  []uuid.UUID
		err     error
		wantHit bool
	}{
		{name: "watched", marked: episodes, wantHit: true},
		{name: "unwatched", unmark: true, marked: episodes},
		{name: "nothing changed"},
		{name: "store error", marked: episodes, err: errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeWatchStateDB{marked: tc.marked, err: tc.err}
			got := make(chan heard, 1)
			h := NewWatchStateHandler(db, nil, slog.Default()).
				WithWatchedHook(func(_ context.Context, user uuid.UUID, ids []uuid.UUID) { got <- heard{user, ids} })
			user := uuid.New()
			call := h.MarkWatched
			if tc.unmark {
				call = h.MarkUnwatched
			}
			call(httptest.NewRecorder(), watchStateRequest(http.MethodPost, db.add("season", "G", uuid.New()), &auth.Claims{UserID: user}))

			select {
			case hit := <-got:
				if !tc.wantHit {
					t.Fatalf("hook called with %+v; want no call", hit)
				}
				if hit.user != user || len(hit.ids) != len(episodes) || hit.ids[0] != episodes[0] {
					t.Errorf("hook got %+v", hit)
				}
			case <-time.After(200 * time.Millisecond):
				if tc.wantHit {
					t.Fatal("hook not called")
				}
			}
		})
	}
}

// ── dismiss ──────────────────────────────────────────────────────────────────

func TestWatchState_Dismiss(t *testing.T) {
	db := &fakeWatchStateDB{dismissRows: 1}
	h := NewWatchStateHandler(db, nil, slog.Default())
	user := uuid.New()
	ep := db.add("episode", "TV-14", uuid.New())

	rec := httptest.NewRecorder()
	h.DismissContinueWatching(rec, watchStateRequest(http.MethodPost, ep, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	if len(db.dismissCalls) != 1 || db.dismissCalls[0].MediaID != ep || db.dismissCalls[0].UserID != user {
		t.Errorf("dismiss calls = %+v", db.dismissCalls)
	}

	track := db.add("track", "G", uuid.New())
	rec = httptest.NewRecorder()
	h.DismissContinueWatching(rec, watchStateRequest(http.MethodPost, track, &auth.Claims{UserID: user}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("track: status %d, want 422", rec.Code)
	}
}

// ── up next ──────────────────────────────────────────────────────────────────

func TestWatchState_UpNext_Response(t *testing.T) {
	season := uuid.New()
	db := &fakeWatchStateDB{}
	db.upNextRows = []gen.ListUpNextEpisodesRow{
		upEp(season, 1, 1, "watched", false, 0, time.Unix(100, 0)),
		upEp(season, 1, 2, "in_progress", true, 60_000, time.Unix(200, 0)),
	}
	h := NewWatchStateHandler(db, nil, slog.Default())
	show := db.add("show", "TV-14", uuid.New())

	rec := httptest.NewRecorder()
	h.UpNext(rec, watchStateRequest(http.MethodGet, show, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data UpNextResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Mode != "resume" || body.Data.Episode == nil {
		t.Fatalf("got %+v", body.Data)
	}
	e := body.Data.Episode
	if e.ID != db.upNextRows[1].ID.String() || e.SeasonID != season.String() ||
		e.SeasonNumber != 1 || e.EpisodeNumber != 2 {
		t.Errorf("episode = %+v", e)
	}
	if e.ViewOffsetMS == nil || *e.ViewOffsetMS != 60_000 {
		t.Errorf("resume offset = %v, want 60000", e.ViewOffsetMS)
	}
	if db.upNextCalls[0].ContainerID != show {
		t.Errorf("container = %s, want the show", db.upNextCalls[0].ContainerID)
	}

	// No episodes → mode none, no episode key.
	db.upNextRows = nil
	rec = httptest.NewRecorder()
	h.UpNext(rec, watchStateRequest(http.MethodGet, show, &auth.Claims{UserID: uuid.New()}))
	var raw map[string]map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if raw["data"]["mode"] != "none" {
		t.Errorf("mode = %v, want none", raw["data"]["mode"])
	}
	if _, ok := raw["data"]["episode"]; ok {
		t.Error("episode must be omitted for mode none")
	}

	// Not a container → 422.
	movie := db.add("movie", "G", uuid.New())
	rec = httptest.NewRecorder()
	h.UpNext(rec, watchStateRequest(http.MethodGet, movie, &auth.Claims{UserID: uuid.New()}))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("movie: status %d, want 422", rec.Code)
	}
}

func upEp(season uuid.UUID, seasonNo, epNo int32, status string, resumable bool, pos int64, at time.Time) gen.ListUpNextEpisodesRow {
	s, e := seasonNo, epNo
	row := gen.ListUpNextEpisodesRow{
		ID:            uuid.New(),
		Title:         "ep",
		SeasonID:      season,
		SeasonNumber:  &s,
		EpisodeNumber: &e,
		Status:        status,
		Resumable:     resumable,
		PositionMs:    pos,
	}
	if !at.IsZero() {
		row.LastActivityAt = pgtype.Timestamptz{Time: at, Valid: true}
	}
	return row
}

func TestPickUpNext(t *testing.T) {
	s0, s1, s2 := uuid.New(), uuid.New(), uuid.New()
	never := time.Time{}
	t1, t2, t3 := time.Unix(1000, 0), time.Unix(2000, 0), time.Unix(3000, 0)

	cases := []struct {
		name     string
		rows     []gen.ListUpNextEpisodesRow
		show     bool
		wantMode string
		wantIdx  int
	}{
		{"no episodes", nil, true, "none", -1},
		{"nothing watched → first", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "unwatched", false, 0, never),
			upEp(s1, 1, 2, "unwatched", false, 0, never),
		}, true, "start", 0},
		{"resume most recently touched", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "in_progress", true, 10, t1),
			upEp(s1, 1, 2, "in_progress", true, 10, t2),
			upEp(s1, 1, 3, "watched", false, 0, t3),
		}, true, "resume", 1},
		{"rewatch in progress still resumes", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", true, 10, t2),
			upEp(s1, 1, 2, "watched", false, 0, t1),
		}, true, "resume", 0},
		{"next after last finished", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", false, 0, t1),
			upEp(s1, 1, 2, "watched", false, 0, t2),
			upEp(s1, 1, 3, "unwatched", false, 0, never),
		}, true, "next", 2},
		{"crosses into the next season", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", false, 0, t1),
			upEp(s2, 2, 1, "unwatched", false, 0, never),
		}, true, "next", 1},
		{"anchor is the most recent, not the furthest", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", false, 0, t1),
			upEp(s1, 1, 2, "watched", false, 0, t3), // rewatched most recently
			upEp(s1, 1, 3, "unwatched", false, 0, never),
			upEp(s1, 1, 4, "watched", false, 0, t2),
			upEp(s1, 1, 5, "unwatched", false, 0, never),
		}, true, "next", 2},
		{"tie (season marked at once) → later episode", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", false, 0, t2),
			upEp(s1, 1, 2, "watched", false, 0, t2),
			upEp(s2, 2, 1, "unwatched", false, 0, never),
		}, true, "next", 2},
		{"nothing after anchor wraps to first gap", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "unwatched", false, 0, never),
			upEp(s1, 1, 2, "watched", false, 0, t1),
		}, true, "next", 0},
		{"all watched → rewatch", []gen.ListUpNextEpisodesRow{
			upEp(s1, 1, 1, "watched", false, 0, t1),
			upEp(s1, 1, 2, "watched", false, 0, t2),
		}, true, "rewatch", 0},
		{"show skips specials", []gen.ListUpNextEpisodesRow{
			upEp(s0, 0, 1, "unwatched", false, 0, never),
			upEp(s1, 1, 1, "watched", false, 0, t1),
			upEp(s1, 1, 2, "unwatched", false, 0, never),
		}, true, "next", 2},
		{"show start skips specials", []gen.ListUpNextEpisodesRow{
			upEp(s0, 0, 1, "unwatched", false, 0, never),
			upEp(s1, 1, 1, "unwatched", false, 0, never),
		}, true, "start", 1},
		{"show that is all specials", []gen.ListUpNextEpisodesRow{
			upEp(s0, 0, 1, "watched", false, 0, t1),
			upEp(s0, 0, 2, "unwatched", false, 0, never),
		}, true, "next", 1},
		{"season scope keeps specials", []gen.ListUpNextEpisodesRow{
			upEp(s0, 0, 1, "unwatched", false, 0, never),
		}, false, "start", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, idx := pickUpNext(tc.rows, tc.show)
			if mode != tc.wantMode || idx != tc.wantIdx {
				t.Errorf("got (%s, %d), want (%s, %d)", mode, idx, tc.wantMode, tc.wantIdx)
			}
		})
	}
}
