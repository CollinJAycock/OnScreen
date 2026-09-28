package requests

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/notification"
)

// ── fakes: season queries + TMDB season list ────────────────────────────────

func (f *fakeDB) ListOwnedEpisodeCountsByShowTMDB(_ context.Context, p gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []gen.ListOwnedEpisodeCountsByShowTMDBRow
	for _, row := range f.owned {
		for _, id := range p.TmdbIds {
			if row.TmdbID == id {
				out = append(out, row)
			}
		}
	}
	return out, nil
}

// MarkMediaRequestSeasonAvailable mirrors the SQL: append the season unless
// it's recorded already or the request isn't in flight.
func (f *fakeDB) MarkMediaRequestSeasonAvailable(_ context.Context, p gen.MarkMediaRequestSeasonAvailableParams) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.reqs[p.ID]
	if !ok || (r.Status != StatusApproved && r.Status != StatusDownloading && r.Status != StatusFailed) {
		return 0, nil
	}
	for _, n := range r.SeasonsAvailable {
		if n == p.Season {
			return 0, nil
		}
	}
	r.SeasonsAvailable = append(append([]int32{}, r.SeasonsAvailable...), p.Season)
	f.reqs[p.ID] = r
	return 1, nil
}

// ownSeason records n distinct episodes of a season in the library.
func (f *fakeDB) ownSeason(tmdbID int32, season, episodes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, row := range f.owned {
		if row.TmdbID == tmdbID && int(row.SeasonNumber) == season {
			f.owned[i].EpisodeCount = int32(episodes)
			return
		}
	}
	f.owned = append(f.owned, gen.ListOwnedEpisodeCountsByShowTMDBRow{
		TmdbID: tmdbID, SeasonNumber: int32(season), EpisodeCount: int32(episodes),
	})
}

// defaultSeasons is what fakeTMDB lists for every show: three aired seasons
// of ten episodes, and a season of specials listed last.
func defaultSeasons(id int) *metadata.TVSeasonList {
	aired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	return &metadata.TVSeasonList{
		TMDBID: id,
		Seasons: []metadata.TVSeasonSummary{
			{Number: 1, Name: "Season 1", EpisodeCount: 10, AirDate: aired},
			{Number: 2, Name: "Season 2", EpisodeCount: 10, AirDate: aired},
			{Number: 3, Name: "Season 3", EpisodeCount: 10, AirDate: aired},
			{Number: 0, Name: "Specials", EpisodeCount: 2, AirDate: aired},
		},
		LastAiredSeason:  3,
		LastAiredEpisode: 10,
	}
}

func (fakeTMDB) GetTVSeasons(_ context.Context, id int) (*metadata.TVSeasonList, error) {
	return defaultSeasons(id), nil
}

// ── pure helpers ────────────────────────────────────────────────────────────

func TestSeasonsOverlap(t *testing.T) {
	cases := []struct {
		a, b []int
		want bool
	}{
		{nil, nil, true},
		{nil, []int{2}, true}, // all seasons overlaps every regular season
		{[]int{2}, nil, true},
		{nil, []int{0}, false}, // …but not specials, which "all" leaves out
		{[]int{0}, nil, false},
		{nil, []int{0, 1}, true},
		{[]int{1, 2}, []int{2, 3}, true},
		{[]int{1, 2}, []int{3, 4}, false},
		{[]int{0}, []int{1}, false},
		{[]int{0}, []int{0, 5}, true},
	}
	for _, tc := range cases {
		if got := SeasonsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("SeasonsOverlap(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRequestSeasonsAndCoversSeason(t *testing.T) {
	show := gen.MediaRequest{Type: TypeShow, Seasons: []byte(`[3,1,3]`)}
	if got := RequestSeasons(show); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Errorf("RequestSeasons = %v, want [1 3]", got)
	}
	if !CoversSeason(show, 3) || CoversSeason(show, 2) {
		t.Error("explicit request must cover exactly its seasons")
	}
	all := gen.MediaRequest{Type: TypeShow}
	if !CoversSeason(all, 4) || CoversSeason(all, 0) {
		t.Error("all-seasons request covers regular seasons, not specials")
	}
	if RequestSeasons(gen.MediaRequest{Type: TypeMovie, Seasons: []byte(`[1]`)}) != nil {
		t.Error("a movie has no seasons")
	}
}

// ── create: duplicate guard + validation ────────────────────────────────────

func TestCreate_SeasonOverlapGuard(t *testing.T) {
	cases := []struct {
		name     string
		existing []int // nil = all seasons
		status   string
		create   []int
		wantErr  bool
	}{
		{"disjoint seasons allowed", []int{1}, StatusDownloading, []int{2, 3}, false},
		{"overlapping seasons refused", []int{1, 2}, StatusPending, []int{2}, true},
		{"all-seasons request blocks any season", nil, StatusApproved, []int{3}, true},
		{"new all-seasons request overlaps an existing season", []int{2}, StatusPending, nil, true},
		{"available request doesn't block", []int{1}, StatusAvailable, []int{1}, false},
		{"declined request doesn't block", nil, StatusDeclined, nil, false},
		{"specials alongside seasons", []int{1, 2}, StatusPending, []int{0}, false},
		{"specials alongside an all-seasons request", nil, StatusDownloading, []int{0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			uid := h.user(gen.GetUserRequestPermissionsRow{})
			existing := h.db.seed(uid, TypeShow, tc.status, h.now.Add(-time.Hour))
			existing.TmdbID = 1399
			existing.Seasons = mustSeasons(t, tc.existing)
			h.db.reqs[existing.ID] = existing

			_, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: tc.create})
			if tc.wantErr {
				if !errors.Is(err, ErrAlreadyRequested) {
					t.Fatalf("err = %v, want ErrAlreadyRequested", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
		})
	}
}

// Another user's request never blocks, and a movie with the same TMDB id
// isn't a show request.
func TestCreate_SeasonGuardScopedToUserAndType(t *testing.T) {
	h := newHarness(t)
	alice := h.user(gen.GetUserRequestPermissionsRow{})
	bob := h.user(gen.GetUserRequestPermissionsRow{})
	r := h.db.seed(bob, TypeShow, StatusPending, h.now)
	r.TmdbID = 1399
	h.db.reqs[r.ID] = r
	m := h.db.seed(alice, TypeMovie, StatusPending, h.now)
	m.TmdbID = 1399
	h.db.reqs[m.ID] = m

	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: alice, Type: TypeShow, TMDBID: 1399}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestCreate_ValidatesAndNormalisesSeasons(t *testing.T) {
	h := newHarness(t)
	uid := h.user(gen.GetUserRequestPermissionsRow{})

	if _, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: []int{2, 9}}); !errors.Is(err, ErrInvalidSeasons) {
		t.Fatalf("season 9 (not on TMDB): err = %v, want ErrInvalidSeasons", err)
	}

	got, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: []int{3, 0, 3, 2}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if string(got.Seasons) != "[0,2,3]" {
		t.Errorf("stored seasons = %s, want [0,2,3]", got.Seasons)
	}

	// A movie's seasons are dropped rather than stored.
	mv, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeMovie, TMDBID: 949, Seasons: []int{1}})
	if err != nil {
		t.Fatalf("Create movie: %v", err)
	}
	if mv.Seasons != nil {
		t.Errorf("movie seasons = %s, want none", mv.Seasons)
	}
}

// A TV request costs one against the quota whatever it names.
func TestCreate_SeasonRequestCountsOnceAgainstQuota(t *testing.T) {
	h := newHarness(t)
	h.defaults = QuotaDefaults{TV: 2, WindowDays: 7}
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	for _, seasons := range [][]int{{1, 2, 3}, {0}} {
		res, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: seasons})
		if err != nil {
			t.Fatalf("Create %v: %v", seasons, err)
		}
		if res.OverQuota {
			t.Fatalf("request %v went over a 2-request quota", seasons)
		}
	}
	st, err := h.svc.Quota(context.Background(), uid)
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if st.TV.Used != 2 {
		t.Errorf("TV quota used = %d, want 2 (one per request)", st.TV.Used)
	}
}

func mustSeasons(t *testing.T, seasons []int) []byte {
	t.Helper()
	b, err := encodeSeasons(seasons)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── Sonarr add: season selection + existing series ──────────────────────────

// sonarrSeriesExistsBody is what Sonarr really answers (400) when asked to add
// a series already in its library.
const sonarrSeriesExistsBody = `[{"propertyName":"TvdbId","errorMessage":"This series has already been added",
  "attemptedValue":371980,"severity":"error","errorCode":"SeriesExistsValidator",
  "formattedMessagePlaceholderValues":{"propertyName":"Tvdb Id","propertyValue":371980}}]`

// seasonSonarr is a fake Sonarr for the add path: lookup answers with three
// seasons, the add either succeeds or is refused because the series is
// already there, and every write is recorded.
type seasonSonarr struct {
	mu sync.Mutex
	// exists: Sonarr already manages the series (id 42). The tvdb listing
	// returns it, and an add is refused with addStatus / addBody (default:
	// Sonarr's real 400 SeriesExistsValidator answer).
	exists    bool
	addStatus int
	addBody   string
	// raced: the series only shows in the tvdb listing once an add was
	// attempted — another add won the race between OnScreen's existence check
	// and its own add. unlisted: it never shows there.
	raced    bool
	unlisted bool
	// lookupID is the id the lookup result carries (Sonarr sets it for a
	// series already in its library); 0 = none.
	lookupID    int
	addAttempts int
	tvdbHits    int
	added       map[string]any
	put         map[string]any
	commands    []map[string]any
	existing    string // GET /api/v3/series/42 body
}

func (s *seasonSonarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, _ := io.ReadAll(r.Body)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/lookup":
		_, _ = fmt.Fprintf(w, `[{"id":%d,"title":"Show","tvdbId":371980,"year":2011,"titleSlug":"show",
		  "seasons":[{"seasonNumber":0,"monitored":false},{"seasonNumber":1,"monitored":true},
		             {"seasonNumber":2,"monitored":true},{"seasonNumber":3,"monitored":true}]}]`, s.lookupID)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/series":
		s.addAttempts++
		_ = json.Unmarshal(raw, &s.added)
		if s.exists {
			status, body := s.addStatus, s.addBody
			if status == 0 {
				status, body = http.StatusBadRequest, sonarrSeriesExistsBody
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":77,"title":"Show"}`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series" && r.URL.Query().Get("tvdbId") == "371980":
		s.tvdbHits++
		if s.exists && !s.unlisted && (!s.raced || s.addAttempts > 0) {
			_, _ = io.WriteString(w, `[{"id":42,"tvdbId":371980,"title":"Show"}]`)
			return
		}
		_, _ = io.WriteString(w, `[]`)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/42":
		_, _ = io.WriteString(w, s.existing)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/42":
		_ = json.Unmarshal(raw, &s.put)
		_, _ = w.Write(raw)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.commands = append(s.commands, body)
		_, _ = io.WriteString(w, `{"id":5}`)
	default:
		http.NotFound(w, r)
	}
}

func pointSonarrAt(t *testing.T, h *harness, handler http.Handler) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc := h.db.services["sonarr"]
	svc.BaseUrl = srv.URL
	h.db.addService(svc)
}

func approveShow(t *testing.T, h *harness, seasons []int) gen.MediaRequest {
	t.Helper()
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: seasons})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: uuid.New()})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return got
}

func addedSeasonFlags(t *testing.T, body map[string]any) map[int]bool {
	t.Helper()
	out := map[int]bool{}
	list, _ := body["seasons"].([]any)
	for _, raw := range list {
		se := raw.(map[string]any)
		out[int(se["seasonNumber"].(float64))] = se["monitored"].(bool)
	}
	return out
}

func TestApprove_AddMonitorsOnlyRequestedSeasons(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{}
	pointSonarrAt(t, h, fs)

	got := approveShow(t, h, []int{2})
	flags := addedSeasonFlags(t, fs.added)
	if want := map[int]bool{0: false, 1: false, 2: true, 3: false}; !reflect.DeepEqual(flags, want) {
		t.Errorf("add seasons = %v, want %v", flags, want)
	}
	opts, _ := fs.added["addOptions"].(map[string]any)
	if _, ok := opts["monitor"]; ok {
		t.Errorf("addOptions.monitor = %v; it overrides the season flags on Sonarr v4 and must be absent", opts["monitor"])
	}
	if opts["searchForMissingEpisodes"] != true {
		t.Errorf("addOptions = %v, want searchForMissingEpisodes", opts)
	}
	if got.ArrItemID == nil || *got.ArrItemID != 77 {
		t.Errorf("arr item = %v, want the new series id 77", got.ArrItemID)
	}
}

func TestApprove_AllSeasonsAddKeepsMonitorAll(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{}
	pointSonarrAt(t, h, fs)

	approveShow(t, h, nil)
	opts, _ := fs.added["addOptions"].(map[string]any)
	if opts["monitor"] != "all" {
		t.Errorf("addOptions.monitor = %v, want all", opts["monitor"])
	}
	if flags := addedSeasonFlags(t, fs.added); !flags[1] || !flags[2] || !flags[3] {
		t.Errorf("add seasons = %v, want every lookup season kept monitored", flags)
	}
}

// existingShowJSON is series 42 as Sonarr returns it: season 1 monitored,
// seasons 2 and 3 not.
const existingShowJSON = `{"id":42,"title":"Show","tvdbId":371980,"monitored":true,
  "path":"/tv/Show","qualityProfileId":9,
  "seasons":[{"seasonNumber":1,"monitored":true},{"seasonNumber":2,"monitored":false},{"seasonNumber":3,"monitored":false}]}`

// "Request more seasons" for a show Sonarr already has: the existence check
// finds it, so no add is sent (Sonarr would refuse it) and the requested
// seasons are switched on and searched on the existing series.
func TestApprove_ExistingSeriesMonitorsAndSearchesRequestedSeasons(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{exists: true, existing: existingShowJSON}
	pointSonarrAt(t, h, fs)

	got := approveShow(t, h, []int{2, 3})
	if fs.addAttempts != 0 {
		t.Errorf("add attempts = %d; a series Sonarr already has must not be re-added", fs.addAttempts)
	}
	assertExistingSeriesMonitored(t, fs, got)
}

// The existence check can miss the series (another add won the race, or a
// build the check can't see it on); Sonarr then refuses the add — with its
// real 400 SeriesExistsValidator answer, or a 409 on some builds — and the
// request still switches to the existing series instead of failing.
func TestApprove_ExistingSeriesAddRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"400 SeriesExistsValidator", http.StatusBadRequest, sonarrSeriesExistsBody},
		{"400 message without an error code", http.StatusBadRequest, `[{"propertyName":"TvdbId","errorMessage":"This series has already been added"}]`},
		{"409", http.StatusConflict, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			fs := &seasonSonarr{exists: true, raced: true, addStatus: tc.status, addBody: tc.body, existing: existingShowJSON}
			pointSonarrAt(t, h, fs)

			got := approveShow(t, h, []int{2, 3})
			if fs.addAttempts != 1 {
				t.Errorf("add attempts = %d, want 1", fs.addAttempts)
			}
			assertExistingSeriesMonitored(t, fs, got)
		})
	}
}

// A lookup result Sonarr marks as already in its library (id > 0) goes
// straight to the existing series.
func TestApprove_ExistingSeriesFromLookupID(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{exists: true, lookupID: 42, existing: existingShowJSON}
	pointSonarrAt(t, h, fs)

	got := approveShow(t, h, []int{2, 3})
	if fs.addAttempts != 0 || fs.tvdbHits != 0 {
		t.Errorf("add attempts = %d, tvdb listings = %d; the lookup id alone identifies the series", fs.addAttempts, fs.tvdbHits)
	}
	assertExistingSeriesMonitored(t, fs, got)
}

// Refused as a duplicate but nowhere to be found: the request stays pending
// for an admin rather than being approved with nothing changed upstream.
func TestApprove_ExistingSeriesNotFoundAfterRefusalStaysPending(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{exists: true, unlisted: true, existing: existingShowJSON}
	pointSonarrAt(t, h, fs)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: []int{2}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: uuid.New()}); !errors.Is(err, ErrArrAddFailed) {
		t.Fatalf("Approve err = %v, want ErrArrAddFailed", err)
	}
	if st := h.db.get(req.ID).Status; st != StatusPending {
		t.Errorf("status = %q, want pending", st)
	}
	if fs.put != nil || len(fs.commands) != 0 {
		t.Errorf("series updated (%v) or searched (%v) though it was never found", fs.put, fs.commands)
	}
}

func assertExistingSeriesMonitored(t *testing.T, fs *seasonSonarr, got gen.MediaRequest) {
	t.Helper()
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q, want downloading", got.Status)
	}
	if got.ArrItemID == nil || *got.ArrItemID != 42 {
		t.Errorf("arr item = %v, want the existing series 42", got.ArrItemID)
	}
	if fs.put == nil {
		t.Fatal("existing series was not updated")
	}
	if flags := addedSeasonFlags(t, fs.put); !reflect.DeepEqual(flags, map[int]bool{1: true, 2: true, 3: true}) {
		t.Errorf("PUT seasons = %v, want 2 and 3 switched on, 1 kept", flags)
	}
	if fs.put["path"] != "/tv/Show" || fs.put["qualityProfileId"] != float64(9) {
		t.Errorf("PUT lost series fields: %v", fs.put)
	}
	want := []map[string]any{
		{"name": "SeasonSearch", "seriesId": float64(42), "seasonNumber": float64(2)},
		{"name": "SeasonSearch", "seriesId": float64(42), "seasonNumber": float64(3)},
	}
	if !reflect.DeepEqual(fs.commands, want) {
		t.Errorf("commands = %v, want %v", fs.commands, want)
	}
}

// A series Sonarr has but OnScreen can't update leaves the request pending
// for an admin instead of approving it with nothing changed upstream.
func TestApprove_ExistingSeriesUpdateFailureStaysPending(t *testing.T) {
	h := newHarness(t)
	fs := &seasonSonarr{exists: true, existing: `not json`}
	pointSonarrAt(t, h, fs)
	uid := h.user(gen.GetUserRequestPermissionsRow{})
	req, err := h.svc.Create(context.Background(), CreateInput{UserID: uid, Type: TypeShow, TMDBID: 1399, Seasons: []int{2}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.svc.Approve(context.Background(), ApproveInput{RequestID: req.ID, AdminID: uuid.New()}); !errors.Is(err, ErrArrAddFailed) {
		t.Fatalf("Approve err = %v, want ErrArrAddFailed", err)
	}
	if st := h.db.reqs[req.ID].Status; st != StatusPending {
		t.Errorf("status = %q, want pending", st)
	}
}

// ── per-season fulfilment ───────────────────────────────────────────────────

// statsSonarr serves one series (id 42) whose per-season statistics a test
// can change between reconcile passes.
type statsSonarr struct {
	mu    sync.Mutex
	stats map[int][2]int // season → {episodeFileCount, episodeCount}
	hits  int
}

func (s *statsSonarr) set(season, files, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats[season] = [2]int{files, count}
}

func (s *statsSonarr) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path != "/api/v3/series/42" {
		http.NotFound(w, r)
		return
	}
	s.hits++
	var seasons []map[string]any
	for n := 0; n <= 3; n++ {
		st := s.stats[n]
		seasons = append(seasons, map[string]any{
			"seasonNumber": n, "monitored": n > 0,
			"statistics": map[string]any{"episodeFileCount": st[0], "episodeCount": st[1], "totalEpisodeCount": 10},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"id": 42, "tvdbId": 371980, "monitored": true, "seasons": seasons})
}

type fulfilHarness struct {
	*harness
	sonarr *statsSonarr
	item   uuid.UUID
	uid    uuid.UUID
}

func newFulfilHarness(t *testing.T) *fulfilHarness {
	t.Helper()
	h := newHarness(t)
	ss := &statsSonarr{stats: map[int][2]int{}}
	pointSonarrAt(t, h, ss)
	item := uuid.New()
	tmdb := int32(1399)
	h.db.libraryItems = append(h.db.libraryItems, gen.ListMediaItemsByTMDBIDsRow{ID: item, TmdbID: &tmdb})
	return &fulfilHarness{harness: h, sonarr: ss, item: item, uid: h.user(gen.GetUserRequestPermissionsRow{})}
}

// inFlight seeds a downloading show request for seasons (nil = all), linked
// to Sonarr series 42 when linked is set.
func (h *fulfilHarness) inFlight(t *testing.T, seasons []int, linked bool) gen.MediaRequest {
	t.Helper()
	svc := h.db.services["sonarr"]
	r := gen.MediaRequest{
		ID: uuid.New(), UserID: h.uid, Type: TypeShow, TmdbID: 1399, Title: "Show",
		Status: StatusDownloading, Seasons: mustSeasons(t, seasons),
		ServiceID: pgUUID(&svc.ID),
		CreatedAt: pgtype.Timestamptz{Time: h.now, Valid: true},
	}
	if linked {
		id := int32(42)
		r.ArrItemID = &id
	}
	h.db.mu.Lock()
	h.db.reqs[r.ID] = r
	h.db.mu.Unlock()
	return r
}

func (h *fulfilHarness) notices(typ string) []notice {
	h.notify.mu.Lock()
	defer h.notify.mu.Unlock()
	var out []notice
	for _, n := range h.notify.sent {
		if n.typ == typ {
			out = append(out, n)
		}
	}
	return out
}

func TestReconcile_SonarrStatsPerSeason(t *testing.T) {
	h := newFulfilHarness(t)
	ctx := context.Background()
	r := h.inFlight(t, []int{1, 2}, true)

	// Season 1 complete in Sonarr and scanned in; season 2 half done.
	h.sonarr.set(1, 10, 10)
	h.sonarr.set(2, 5, 10)
	h.db.ownSeason(1399, 1, 10)
	h.db.ownSeason(1399, 2, 5)
	h.svc.ReconcileFulfillments(ctx)

	got := h.db.get(r.ID)
	if got.Status != StatusDownloading {
		t.Fatalf("status = %q, want still downloading (season 2 incomplete)", got.Status)
	}
	if !reflect.DeepEqual(got.SeasonsAvailable, []int32{1}) {
		t.Errorf("seasons_available = %v, want [1]", got.SeasonsAvailable)
	}
	seasonNotices := h.notices(notification.TypeRequestSeasonAvailable)
	if len(seasonNotices) != 1 || seasonNotices[0].title != "Season 1 now available: Show" {
		t.Fatalf("season notices = %+v, want one for season 1", seasonNotices)
	}
	if n := h.notices(notification.TypeRequestAvailable); len(n) != 0 {
		t.Errorf("request_available sent early: %+v", n)
	}

	// Another pass with nothing new sends nothing.
	h.svc.ReconcileFulfillments(ctx)
	if n := h.notices(notification.TypeRequestSeasonAvailable); len(n) != 1 {
		t.Errorf("season notices after a no-op pass = %d, want still 1", len(n))
	}

	// Season 2 lands: the request completes with request_available only.
	h.sonarr.set(2, 10, 10)
	h.db.ownSeason(1399, 2, 10)
	h.svc.ReconcileFulfillments(ctx)
	got = h.db.get(r.ID)
	if got.Status != StatusAvailable || !got.FulfilledItemID.Valid || got.FulfilledItemID.Bytes != h.item {
		t.Fatalf("status = %q item = %v, want available for the show", got.Status, got.FulfilledItemID)
	}
	if n := h.notices(notification.TypeRequestSeasonAvailable); len(n) != 1 {
		t.Errorf("season notices = %d, want 1 (the last season is covered by request_available)", len(n))
	}
	if n := h.notices(notification.TypeRequestAvailable); len(n) != 1 {
		t.Errorf("request_available notices = %d, want 1", len(n))
	}
	if !reflect.DeepEqual(got.SeasonsAvailable, []int32{1, 2}) {
		t.Errorf("seasons_available = %v, want [1 2]", got.SeasonsAvailable)
	}
}

// Sonarr having the files isn't enough: the season must be in the library.
func TestReconcile_SonarrCompleteButNotScannedWaits(t *testing.T) {
	h := newFulfilHarness(t)
	r := h.inFlight(t, []int{3}, true)
	h.sonarr.set(3, 10, 10)
	h.svc.ReconcileFulfillments(context.Background())
	if got := h.db.get(r.ID); got.Status != StatusDownloading {
		t.Errorf("status = %q, want downloading until the library has the season", got.Status)
	}
}

// Without a Sonarr link the library is compared with TMDB's aired episodes.
func TestReconcile_LibraryVersusTMDBFallback(t *testing.T) {
	h := newFulfilHarness(t)
	ctx := context.Background()
	r := h.inFlight(t, []int{2, 3}, false)

	h.db.ownSeason(1399, 2, 10)
	h.db.ownSeason(1399, 3, 9)
	h.svc.ReconcileFulfillments(ctx)
	got := h.db.get(r.ID)
	if got.Status != StatusDownloading || !reflect.DeepEqual(got.SeasonsAvailable, []int32{2}) {
		t.Fatalf("status=%q seasons_available=%v, want downloading with [2]", got.Status, got.SeasonsAvailable)
	}
	if h.sonarr.hits != 0 {
		t.Errorf("sonarr asked %d times for an unlinked request", h.sonarr.hits)
	}

	h.db.ownSeason(1399, 3, 10)
	h.svc.ReconcileFulfillments(ctx)
	if got := h.db.get(r.ID); got.Status != StatusAvailable {
		t.Errorf("status = %q, want available", got.Status)
	}
}

// The regression the season work fixes: one imported episode used to close an
// all-seasons request. It now stays open until every aired season is in.
func TestReconcile_AllSeasonsNotClosedByOneEpisode(t *testing.T) {
	h := newFulfilHarness(t)
	ctx := context.Background()
	r := h.inFlight(t, nil, true)
	h.sonarr.set(1, 1, 10)
	h.sonarr.set(2, 0, 10)
	h.sonarr.set(3, 0, 10)
	h.db.ownSeason(1399, 1, 1)
	h.svc.ReconcileFulfillments(ctx)
	if got := h.db.get(r.ID); got.Status != StatusDownloading {
		t.Fatalf("status = %q after one episode, want downloading", got.Status)
	}

	// Everything lands: the all-seasons request completes exactly as before.
	for n := 1; n <= 3; n++ {
		h.sonarr.set(n, 10, 10)
		h.db.ownSeason(1399, n, 10)
	}
	h.svc.ReconcileFulfillments(ctx)
	if got := h.db.get(r.ID); got.Status != StatusAvailable {
		t.Errorf("status = %q, want available once every season is in", got.Status)
	}
	if n := h.notices(notification.TypeRequestAvailable); len(n) != 1 {
		t.Errorf("request_available notices = %d, want 1", len(n))
	}
}

// An all-seasons request covers aired seasons only: a season Sonarr lists
// with nothing aired doesn't hold the request open.
func TestReconcile_AllSeasonsIgnoresUnairedSeasons(t *testing.T) {
	h := newFulfilHarness(t)
	r := h.inFlight(t, nil, true)
	h.sonarr.set(1, 10, 10)
	h.sonarr.set(2, 10, 10)
	h.sonarr.set(3, 0, 0) // announced, nothing aired
	h.db.ownSeason(1399, 1, 10)
	h.db.ownSeason(1399, 2, 10)
	h.svc.ReconcileFulfillments(context.Background())
	if got := h.db.get(r.ID); got.Status != StatusAvailable {
		t.Errorf("status = %q, want available", got.Status)
	}
}

// Sonarr unreachable: the pass falls back to the library and TMDB rather than
// stalling the request.
func TestReconcile_SonarrDownFallsBack(t *testing.T) {
	h := newFulfilHarness(t)
	svc := h.db.services["sonarr"]
	svc.BaseUrl = "http://127.0.0.1:1"
	h.db.addService(svc)
	r := h.inFlight(t, []int{1}, true)
	h.db.ownSeason(1399, 1, 10)
	h.svc.ReconcileFulfillments(context.Background())
	if got := h.db.get(r.ID); got.Status != StatusAvailable {
		t.Errorf("status = %q, want available via the TMDB fallback", got.Status)
	}
}

// MarkFulfilled (the per-title form) applies the same season rules.
func TestMarkFulfilled_ShowUsesSeasonRules(t *testing.T) {
	h := newFulfilHarness(t)
	r := h.inFlight(t, []int{1, 2}, false)
	h.db.ownSeason(1399, 1, 3)
	h.svc.MarkFulfilled(context.Background(), TypeShow, 1399, h.item)
	if got := h.db.get(r.ID); got.Status != StatusDownloading {
		t.Fatalf("status = %q, want downloading (one partial season)", got.Status)
	}
	h.db.ownSeason(1399, 1, 10)
	h.db.ownSeason(1399, 2, 10)
	h.svc.MarkFulfilled(context.Background(), TypeShow, 1399, h.item)
	if got := h.db.get(r.ID); got.Status != StatusAvailable {
		t.Errorf("status = %q, want available", got.Status)
	}
}

// Two passes racing on the same season send one notice between them.
func TestReconcile_SeasonNoticeOnceUnderConcurrency(t *testing.T) {
	h := newFulfilHarness(t)
	h.inFlight(t, []int{1, 2}, false)
	h.db.ownSeason(1399, 1, 10)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.svc.ReconcileFulfillments(context.Background())
		}()
	}
	wg.Wait()
	if n := h.notices(notification.TypeRequestSeasonAvailable); len(n) != 1 {
		t.Errorf("season notices = %d, want exactly 1", len(n))
	}
}

// Movies are untouched by the season logic.
func TestReconcile_MovieStillFulfilledByPresence(t *testing.T) {
	h := newFulfilHarness(t)
	item := uuid.New()
	tmdb := int32(949)
	h.db.libraryItems = append(h.db.libraryItems, gen.ListMediaItemsByTMDBIDsRow{ID: item, TmdbID: &tmdb})
	m := h.db.seed(h.uid, TypeMovie, StatusDownloading, h.now)
	m.TmdbID = 949
	h.db.reqs[m.ID] = m
	h.svc.ReconcileFulfillments(context.Background())
	if got := h.db.get(m.ID); got.Status != StatusAvailable {
		t.Errorf("movie status = %q, want available", got.Status)
	}
}

// ── download sync: season-scoped status ─────────────────────────────────────

func TestSeasonsIdleStatus_CountsRequestedSeasonsOnly(t *testing.T) {
	series := seriesWithSeasons(map[int][2]int{1: {10, 10}, 2: {0, 10}, 3: {4, 8}})
	st := seasonsIdleStatus(series, nil, []int{1})
	if st.State != DownloadImportPending {
		t.Errorf("season 1 only: state = %q, want import_pending", st.State)
	}
	st = seasonsIdleStatus(series, nil, []int{3})
	if st.State != DownloadSearching || st.Message != "4 of 8 episodes downloaded" {
		t.Errorf("season 3 only: %+v, want searching '4 of 8 episodes downloaded'", st)
	}
	// All seasons: the series totals, as before.
	if st := seasonsIdleStatus(series, nil, nil); st.State == DownloadImportPending {
		t.Errorf("all seasons: state = %q, want not complete", st.State)
	}
}

func TestQueueRowsForSeasons(t *testing.T) {
	rows := queueRows(t, `[
	  {"id":1,"seriesId":42,"seasonNumber":1},
	  {"id":2,"seriesId":42,"episode":{"seasonNumber":2,"episodeNumber":1}},
	  {"id":3,"seriesId":42}]`)
	got := queueRowsForSeasons(rows, []int{2})
	var ids []int
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if !reflect.DeepEqual(ids, []int{2, 3}) {
		t.Errorf("kept rows = %v, want [2 3] (season 2 plus the row with no season)", ids)
	}
	if len(queueRowsForSeasons(rows, nil)) != 3 {
		t.Error("an all-seasons request keeps every row")
	}
}

// A season request linked to a series with other seasons downloading only
// reports its own season.
func TestSyncDownloads_SeasonRequestIgnoresOtherSeasonsQueue(t *testing.T) {
	h := newSyncHarness(t)
	r := h.request(TypeShow, 1399, StatusDownloading, 42)
	r.Seasons = []byte(`[1]`)
	h.db.reqs[r.ID] = r
	h.sonarr.set(func(a *statusArr) {
		a.queue = []string{`{"id":9,"seriesId":42,"seasonNumber":2,"status":"downloading","size":100,"sizeleft":50,"downloadId":"x"}`}
		a.items[42] = seriesJSON(map[int][2]int{1: {10, 10}, 2: {0, 10}})
	})
	h.sync(t)
	if got := state(h.db.get(r.ID)); got != DownloadImportPending {
		t.Errorf("state = %q, want import_pending (season 1 is complete; season 2's download isn't this request's)", got)
	}
}

func seriesWithSeasons(stats map[int][2]int) *arr.Series {
	var s arr.Series
	if err := json.Unmarshal([]byte(seriesJSON(stats)), &s); err != nil {
		panic(err)
	}
	return &s
}

func seriesJSON(stats map[int][2]int) string {
	var seasons []map[string]any
	files, count := 0, 0
	for n := 0; n <= 5; n++ {
		st, ok := stats[n]
		if !ok {
			continue
		}
		files += st[0]
		count += st[1]
		seasons = append(seasons, map[string]any{
			"seasonNumber": n, "monitored": true,
			"statistics": map[string]any{"episodeFileCount": st[0], "episodeCount": st[1]},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"id": 42, "tvdbId": 371980, "monitored": true, "seasons": seasons,
		"statistics": map[string]any{"episodeFileCount": files, "episodeCount": count},
	})
	return string(b)
}

func queueRows(t *testing.T, raw string) []arr.QueueRecord {
	t.Helper()
	var out []arr.QueueRecord
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
