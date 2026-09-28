package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/tmdb"
	"github.com/onscreen/onscreen/internal/requests"
)

// Season-level TV requests at the HTTP layer: the season picker endpoint
// (GET /discover/tv/{tmdb_id}/seasons), missing_seasons on discover results,
// the season-aware duplicate guard on POST /requests, and seasons_available
// on the request DTO.

// ── fakes: the season surface of requests.DB / requests.TMDB ─────────────────

func (f *rqFakeDB) ListOwnedEpisodeCountsByShowTMDB(context.Context, gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error) {
	return nil, nil
}
func (f *rqFakeDB) MarkMediaRequestSeasonAvailable(context.Context, gen.MarkMediaRequestSeasonAvailableParams) (int64, error) {
	return 0, nil
}

func seasonFixture(id int) *metadata.TVSeasonList {
	aired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	return &metadata.TVSeasonList{
		TMDBID: id,
		Seasons: []metadata.TVSeasonSummary{
			{Number: 1, Name: "Season 1", EpisodeCount: 10, AirDate: aired, PosterURL: "https://img/s1.jpg"},
			{Number: 2, Name: "Season 2", EpisodeCount: 10, AirDate: aired},
			{Number: 3, Name: "Season 3", EpisodeCount: 8},
			{Number: 0, Name: "Specials", EpisodeCount: 2, AirDate: aired},
		},
		LastAiredSeason:  2,
		LastAiredEpisode: 10,
	}
}

func (rqFakeTMDB) GetTVSeasons(_ context.Context, id int) (*metadata.TVSeasonList, error) {
	return seasonFixture(id), nil
}

// seasonReqDB is rqFakeDB with the user's active show requests visible to the
// duplicate guard (the base fake reports none).
type seasonReqDB struct {
	*rqFakeDB
	active []gen.MediaRequest
}

// CreateMediaRequest keeps the stored season list (the base fake drops it).
func (f *seasonReqDB) CreateMediaRequest(ctx context.Context, p gen.CreateMediaRequestParams) (gen.MediaRequest, error) {
	r, err := f.rqFakeDB.CreateMediaRequest(ctx, p)
	r.Seasons = p.Seasons
	return r, err
}

func (f *seasonReqDB) FindActiveRequestsForUserByTMDB(_ context.Context, p gen.FindActiveRequestsForUserByTMDBParams) ([]gen.MediaRequest, error) {
	var out []gen.MediaRequest
	for _, r := range f.active {
		if r.UserID == p.UserID {
			out = append(out, r)
		}
	}
	return out, nil
}

// ── POST /requests: seasons ──────────────────────────────────────────────────

func postShow(h *RequestHandler, uid uuid.UUID, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Create(rec, rqAs(httptest.NewRequest("POST", "/api/v1/requests", strings.NewReader(body)), uid, false))
	return rec
}

func TestRequests_Create_SeasonOverlap(t *testing.T) {
	uid := uuid.New()
	base := &rqFakeDB{perms: map[uuid.UUID]gen.GetUserRequestPermissionsRow{uid: {CanRequest: true}}}
	db := &seasonReqDB{rqFakeDB: base, active: []gen.MediaRequest{{
		ID: uuid.New(), UserID: uid, Type: requests.TypeShow, TmdbID: 1399, Status: requests.StatusDownloading,
		Seasons: []byte(`[1,2]`),
	}}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRequestHandler(requests.NewService(db, rqFakeTMDB{}, nil, logger), logger)

	cases := []struct {
		name     string
		body     string
		wantCode int
		wantText string
	}{
		{"overlapping season", `{"type":"show","tmdb_id":1399,"seasons":[2,3]}`, http.StatusConflict, "one of these seasons"},
		{"all seasons overlaps", `{"type":"show","tmdb_id":1399}`, http.StatusConflict, "ALREADY_REQUESTED"},
		{"season not on TMDB", `{"type":"show","tmdb_id":1399,"seasons":[7]}`, http.StatusUnprocessableEntity, "season 7 is not listed on TMDB"},
		{"more seasons", `{"type":"show","tmdb_id":1399,"seasons":[3]}`, http.StatusCreated, `"seasons":[3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postShow(h, uid, tc.body)
			if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Errorf("status = %d body=%s, want %d containing %q", rec.Code, rec.Body, tc.wantCode, tc.wantText)
			}
		})
	}
}

// ── request DTO: seasons_available ──────────────────────────────────────────

func TestRequestDTO_SeasonsAvailable(t *testing.T) {
	show := toRequestDTO(gen.MediaRequest{Type: requests.TypeShow, Seasons: []byte(`[1,2,3]`), SeasonsAvailable: []int32{1, 2}})
	b, _ := json.Marshal(show)
	if !strings.Contains(string(b), `"seasons_available":[1,2]`) || !strings.Contains(string(b), `"seasons":[1,2,3]`) {
		t.Errorf("show DTO = %s, want seasons and seasons_available", b)
	}
	none := toRequestDTO(gen.MediaRequest{Type: requests.TypeShow})
	b, _ = json.Marshal(none)
	if !strings.Contains(string(b), `"seasons_available":[]`) {
		t.Errorf("show DTO with nothing yet = %s, want seasons_available []", b)
	}
	movie := toRequestDTO(gen.MediaRequest{Type: requests.TypeMovie})
	b, _ = json.Marshal(movie)
	if strings.Contains(string(b), "seasons_available") {
		t.Errorf("movie DTO = %s, want no seasons_available", b)
	}
	// Status stays one of the values clients already parse.
	if show.Status != "" && show.Status != requests.StatusDownloading {
		t.Errorf("unexpected status %q", show.Status)
	}
}

// ── GET /discover/tv/{tmdb_id}/seasons ───────────────────────────────────────

type fakeSeasonTMDB struct {
	err   error
	calls int
}

func (f *fakeSeasonTMDB) GetTVSeasons(_ context.Context, id int) (*metadata.TVSeasonList, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return seasonFixture(id), nil
}

// fakeSeasonDB records the scope it was asked with and answers per library.
type fakeSeasonDB struct {
	rows     map[uuid.UUID][]gen.ListOwnedEpisodeCountsByShowTMDBRow // library → rows
	gotLibs  []uuid.UUID
	gotRank  *int32
	gotTMDBs []int32
	lookedUp bool
}

func (f *fakeSeasonDB) ListOwnedEpisodeCountsByShowTMDB(_ context.Context, p gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error) {
	f.lookedUp = true
	f.gotLibs, f.gotRank, f.gotTMDBs = p.LibraryIds, p.MaxRatingRank, p.TmdbIds
	var out []gen.ListOwnedEpisodeCountsByShowTMDBRow
	for lib, rows := range f.rows {
		if p.LibraryIds != nil && !containsUUID(p.LibraryIds, lib) {
			continue
		}
		out = append(out, rows...)
	}
	return out, nil
}

func containsUUID(list []uuid.UUID, id uuid.UUID) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}

// seasonReqLookup is the caller's active requests as DiscoverRequestLookup.
type seasonReqLookup struct{ reqs []gen.MediaRequest }

func (l seasonReqLookup) FindActiveForUser(context.Context, uuid.UUID, string, int) (*gen.MediaRequest, error) {
	return nil, nil
}
func (l seasonReqLookup) FindActiveForUserBatch(context.Context, uuid.UUID, []int) ([]gen.MediaRequest, error) {
	return l.reqs, nil
}

type seasonAccess struct {
	allowed map[uuid.UUID]struct{}
	err     error
}

func (a seasonAccess) CanAccessLibrary(context.Context, uuid.UUID, uuid.UUID, bool) (bool, error) {
	return true, nil
}
func (a seasonAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if a.err != nil {
		return nil, a.err
	}
	if isAdmin {
		return nil, nil
	}
	return a.allowed, nil
}

var (
	seasonLibVisible = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	seasonLibHidden  = uuid.MustParse("00000000-0000-0000-0000-0000000000b2")
)

func seasonDBFixture() *fakeSeasonDB {
	return &fakeSeasonDB{rows: map[uuid.UUID][]gen.ListOwnedEpisodeCountsByShowTMDBRow{
		seasonLibVisible: {{TmdbID: 1399, SeasonNumber: 1, EpisodeCount: 10}},
		seasonLibHidden:  {{TmdbID: 1399, SeasonNumber: 2, EpisodeCount: 10}},
	}}
}

func seasonsRequest(uid uuid.UUID, id string, claims *auth.Claims) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/discover/tv/"+id+"/seasons", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("tmdb_id", id)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if claims != nil {
		ctx = middleware.WithClaims(ctx, claims)
	}
	return req.WithContext(ctx)
}

type seasonsBody struct {
	Data []SeasonInfo `json:"data"`
}

func TestDiscoverSeasons_ShapeOwnershipAndRequests(t *testing.T) {
	uid := uuid.New()
	sdb := seasonDBFixture()
	reqID := uuid.New()
	reqs := seasonReqLookup{reqs: []gen.MediaRequest{
		{ID: reqID, UserID: uid, Type: requests.TypeShow, TmdbID: 1399, Status: requests.StatusPending, Seasons: []byte(`[3]`)},
		// A movie with the same TMDB id is not a season request.
		{ID: uuid.New(), UserID: uid, Type: requests.TypeMovie, TmdbID: 1399, Status: requests.StatusPending},
	}}
	h := NewDiscoverHandler(nil, nil, reqs, slog.Default()).
		WithAccess(seasonAccess{allowed: map[uuid.UUID]struct{}{seasonLibVisible: {}}}).
		WithSeasons(&fakeSeasonTMDB{}, sdb)

	rec := httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid, MaxContentRating: "TV-14"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var body seasonsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var order []int
	for _, s := range body.Data {
		order = append(order, s.SeasonNumber)
	}
	if !reflect.DeepEqual(order, []int{1, 2, 3, 0}) {
		t.Fatalf("season order = %v, want specials last", order)
	}
	s1, s2, s3 := body.Data[0], body.Data[1], body.Data[2]
	if s1.OwnedEpisodes != 10 || s1.AiredEpisodes != 10 || s1.AirDate == nil || *s1.AirDate != "2020-01-01" || s1.PosterURL == "" {
		t.Errorf("season 1 = %+v", s1)
	}
	// Season 2's episodes live in a library the caller can't see.
	if s2.OwnedEpisodes != 0 {
		t.Errorf("season 2 owned = %d, want 0 (hidden library)", s2.OwnedEpisodes)
	}
	if s3.AiredEpisodes != 0 || s3.AirDate != nil {
		t.Errorf("season 3 = %+v, want unaired with no date", s3)
	}
	if s3.Requested == nil || *s3.Requested != requests.StatusPending || s3.RequestID == nil || *s3.RequestID != reqID {
		t.Errorf("season 3 requested = %v/%v, want pending by %s", s3.Requested, s3.RequestID, reqID)
	}
	if s1.Requested != nil {
		t.Errorf("season 1 requested = %v, want null", *s1.Requested)
	}
	// The library lookup was scoped to the caller.
	if !reflect.DeepEqual(sdb.gotLibs, []uuid.UUID{seasonLibVisible}) || sdb.gotRank == nil {
		t.Errorf("scope = libs %v rank %v, want the visible library and a rating ceiling", sdb.gotLibs, sdb.gotRank)
	}
	// Raw JSON carries null for "not requested".
	if !strings.Contains(rec.Body.String(), `"requested":null`) {
		t.Errorf("body = %s, want requested:null on unrequested seasons", rec.Body)
	}
}

func TestDiscoverSeasons_AllSeasonsRequestCoversRegularSeasonsOnly(t *testing.T) {
	uid := uuid.New()
	reqs := seasonReqLookup{reqs: []gen.MediaRequest{{ID: uuid.New(), UserID: uid, Type: requests.TypeShow, TmdbID: 1399, Status: requests.StatusDownloading}}}
	h := NewDiscoverHandler(nil, nil, reqs, slog.Default()).WithSeasons(&fakeSeasonTMDB{}, seasonDBFixture())
	rec := httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid, IsAdmin: true}))
	var body seasonsBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, s := range body.Data {
		requested := s.Requested != nil
		if requested != (s.SeasonNumber > 0) {
			t.Errorf("season %d requested = %v, want %v", s.SeasonNumber, requested, s.SeasonNumber > 0)
		}
	}
	// An admin sees every library.
	if s2 := body.Data[1]; s2.OwnedEpisodes != 10 {
		t.Errorf("admin season 2 owned = %d, want 10", s2.OwnedEpisodes)
	}
}

func TestDiscoverSeasons_Errors(t *testing.T) {
	uid := uuid.New()
	claims := &auth.Claims{UserID: uid}
	cases := []struct {
		name   string
		h      *DiscoverHandler
		id     string
		claims *auth.Claims
		want   int
	}{
		{"unauthenticated", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{}, seasonDBFixture()), "1399", nil, http.StatusUnauthorized},
		{"not wired", NewDiscoverHandler(nil, nil, nil, slog.Default()), "1399", claims, http.StatusServiceUnavailable},
		{"bad id", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{}, seasonDBFixture()), "abc", claims, http.StatusBadRequest},
		{"zero id", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{}, seasonDBFixture()), "0", claims, http.StatusBadRequest},
		{"tmdb 404", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{err: tmdbNotFound(t)}, seasonDBFixture()), "1399", claims, http.StatusNotFound},
		{"tmdb down", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{err: errors.New("boom")}, seasonDBFixture()), "1399", claims, http.StatusBadGateway},
		{"tmdb not configured", NewDiscoverHandler(nil, nil, nil, slog.Default()).WithSeasons(&fakeSeasonTMDB{err: fmt.Errorf("adapter: %w", ErrSeasonsTMDBUnavailable)}, seasonDBFixture()), "1399", claims, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.h.Seasons(rec, seasonsRequest(uid, tc.id, tc.claims))
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// A failure reading the caller's grants fails closed: nothing owned.
func TestDiscoverSeasons_AccessErrorFailsClosed(t *testing.T) {
	uid := uuid.New()
	sdb := seasonDBFixture()
	h := NewDiscoverHandler(nil, nil, nil, slog.Default()).
		WithAccess(seasonAccess{err: errors.New("acl down")}).
		WithSeasons(&fakeSeasonTMDB{}, sdb)
	rec := httptest.NewRecorder()
	h.Seasons(rec, seasonsRequest(uid, "1399", &auth.Claims{UserID: uid}))
	var body seasonsBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	for _, s := range body.Data {
		if s.OwnedEpisodes != 0 {
			t.Errorf("season %d owned = %d with the ACL unreadable, want 0", s.SeasonNumber, s.OwnedEpisodes)
		}
	}
	if sdb.lookedUp {
		t.Error("library was queried without a scope")
	}
}

// tmdbNotFound is the TMDB client's 404 error as the adapter returns it.
func tmdbNotFound(t *testing.T) error {
	t.Helper()
	return fmt.Errorf("tmdb tv seasons 1: %w", tmdb.ErrNotFound)
}

// ── discover search: missing_seasons ─────────────────────────────────────────

type seasonDiscoverDB struct {
	rows []gen.ListMediaItemsByTMDBIDsRow
}

func (d seasonDiscoverDB) ListMediaItemsByTMDBIDs(_ context.Context, p gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	if p.Type != "show" {
		return nil, nil
	}
	return d.rows, nil
}

type seasonSearchTMDB struct{ results []tmdb.DiscoverResult }

func (s seasonSearchTMDB) SearchMulti(context.Context, string, int) ([]tmdb.DiscoverResult, error) {
	return s.results, nil
}

func TestDiscoverSearch_MissingSeasons(t *testing.T) {
	uid := uuid.New()
	owned := int32(1399)
	sdb := &fakeSeasonDB{rows: map[uuid.UUID][]gen.ListOwnedEpisodeCountsByShowTMDBRow{
		seasonLibVisible: {
			{TmdbID: 1399, SeasonNumber: 1, EpisodeCount: 10},
			{TmdbID: 1399, SeasonNumber: 2, EpisodeCount: 4},
			{TmdbID: 2000, SeasonNumber: 1, EpisodeCount: 10},
			{TmdbID: 2000, SeasonNumber: 2, EpisodeCount: 10},
		},
	}}
	full := int32(2000)
	st := &fakeSeasonTMDB{}
	h := NewDiscoverHandler(
		seasonDiscoverDB{rows: []gen.ListMediaItemsByTMDBIDsRow{{ID: uuid.New(), TmdbID: &owned}, {ID: uuid.New(), TmdbID: &full}}},
		seasonSearchTMDB{results: []tmdb.DiscoverResult{
			{MediaType: "tv", TMDBID: 1399, Title: "Partly owned"},
			{MediaType: "tv", TMDBID: 2000, Title: "Fully owned"},
			{MediaType: "tv", TMDBID: 3000, Title: "Not in library"},
			{MediaType: "movie", TMDBID: 949, Title: "Heat"},
		}},
		nil, slog.Default(),
	).WithSeasons(st, sdb)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/discover/search?q=x", nil)
	req = req.WithContext(middleware.WithClaims(req.Context(), &auth.Claims{UserID: uid}))
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	byID := map[float64]map[string]any{}
	for _, it := range body.Data {
		byID[it["tmdb_id"].(float64)] = it
	}
	partly := byID[1399]
	if partly["in_library"] != true || partly["partially_available"] != true {
		t.Errorf("partly owned = %v, want in_library + partially_available", partly)
	}
	if got := partly["missing_seasons"]; !reflect.DeepEqual(got, []any{float64(2)}) {
		t.Errorf("missing_seasons = %v, want [2] (season 3 hasn't aired)", got)
	}
	fully := byID[2000]
	if fully["partially_available"] != false || !reflect.DeepEqual(fully["missing_seasons"], []any{}) {
		t.Errorf("fully owned = %v, want missing_seasons [] and not partial", fully)
	}
	if _, ok := byID[3000]["missing_seasons"]; ok {
		t.Errorf("show not in library carries missing_seasons: %v", byID[3000])
	}
	if _, ok := byID[949]["missing_seasons"]; ok || byID[949]["partially_available"] != false {
		t.Errorf("movie = %v, want no missing_seasons and partially_available false", byID[949])
	}
	if st.calls != 2 {
		t.Errorf("TMDB season lookups = %d, want 2 (in-library shows only)", st.calls)
	}
}
