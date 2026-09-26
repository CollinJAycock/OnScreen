package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/api/middleware"
	"github.com/onscreen/onscreen/internal/arr"
	"github.com/onscreen/onscreen/internal/auth"
	"github.com/onscreen/onscreen/internal/contentrating"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/library"
)

// ── fakes ─────────────────────────────────────────────────────────────────

// upcomingLinkRow is a media item the fake DB can link to, carrying the
// columns ListMediaItemsByTMDBIDs filters on.
type upcomingLinkRow struct {
	id        uuid.UUID
	libraryID uuid.UUID
	itemType  string
	tmdbID    int32
	rating    string
}

type fakeUpcomingDB struct {
	mu       sync.Mutex
	services []gen.ArrService
	items    []upcomingLinkRow
	lookups  []gen.ListMediaItemsByTMDBIDsParams
}

func (f *fakeUpcomingDB) ListArrServices(context.Context) ([]gen.ArrService, error) {
	return f.services, nil
}

// ListMediaItemsByTMDBIDs mirrors the SQL: type + tmdb match, optionally
// scoped to library_ids and to a max rating rank.
func (f *fakeUpcomingDB) ListMediaItemsByTMDBIDs(_ context.Context, arg gen.ListMediaItemsByTMDBIDsParams) ([]gen.ListMediaItemsByTMDBIDsRow, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, arg)
	f.mu.Unlock()
	var out []gen.ListMediaItemsByTMDBIDsRow
	for _, it := range f.items {
		if it.itemType != arg.Type || !slices.Contains(arg.TmdbIds, it.tmdbID) {
			continue
		}
		if arg.LibraryIds != nil && !slices.Contains(arg.LibraryIds, it.libraryID) {
			continue
		}
		if arg.MaxRatingRank != nil && int32(contentrating.Rank(it.rating)) > *arg.MaxRatingRank {
			continue
		}
		tmdb := it.tmdbID
		out = append(out, gen.ListMediaItemsByTMDBIDsRow{ID: it.id, LibraryID: it.libraryID, TmdbID: &tmdb})
	}
	return out, nil
}

type fakeUpcomingLibs struct{ libs []library.Library }

func (f *fakeUpcomingLibs) List(context.Context) ([]library.Library, error) { return f.libs, nil }

// fakeUpcomingAccess grants the listed libraries to every non-admin.
type fakeUpcomingAccess struct{ allowed []uuid.UUID }

func (f *fakeUpcomingAccess) CanAccessLibrary(_ context.Context, _, libraryID uuid.UUID, isAdmin bool) (bool, error) {
	return isAdmin || slices.Contains(f.allowed, libraryID), nil
}

func (f *fakeUpcomingAccess) AllowedLibraryIDs(_ context.Context, _ uuid.UUID, isAdmin bool) (map[uuid.UUID]struct{}, error) {
	if isAdmin {
		return nil, nil
	}
	set := make(map[uuid.UUID]struct{}, len(f.allowed))
	for _, id := range f.allowed {
		set[id] = struct{}{}
	}
	return set, nil
}

type fakeUpcomingPaths map[string]string

func (f fakeUpcomingPaths) ArrPathMappings(context.Context) map[string]string { return f }

// fakeArr is an httptest Radarr/Sonarr that serves a fixed calendar body
// and counts hits.
type fakeArr struct {
	srv    *httptest.Server
	hits   atomic.Int32
	status int
	body   string
}

func newFakeArr(t *testing.T, body string) *fakeArr {
	t.Helper()
	f := &fakeArr{status: http.StatusOK, body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if r.URL.Path != "/api/v3/calendar" || r.Header.Get("X-Api-Key") != "key" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, f.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func arrSvc(name, kind, baseURL string) gen.ArrService {
	return gen.ArrService{ID: uuid.New(), Name: name, Kind: kind, BaseUrl: baseURL, ApiKey: "key", Enabled: true}
}

type upcomingFixture struct {
	h      *UpcomingHandler
	db     *fakeUpcomingDB
	libs   *fakeUpcomingLibs
	access *fakeUpcomingAccess
	paths  fakeUpcomingPaths
	clock  *time.Time
}

func newUpcomingFixture(services ...gen.ArrService) *upcomingFixture {
	f := &upcomingFixture{
		db:     &fakeUpcomingDB{services: services},
		libs:   &fakeUpcomingLibs{},
		access: &fakeUpcomingAccess{},
		paths:  fakeUpcomingPaths{},
	}
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	f.clock = &now
	f.h = NewUpcomingHandler(f.db, f.libs, f.access, f.paths, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.h.now = func() time.Time { return *f.clock }
	return f
}

var (
	upcomingAdmin = &auth.Claims{UserID: uuid.New(), IsAdmin: true}
	upcomingUser  = &auth.Claims{UserID: uuid.New()}
	upcomingKid   = &auth.Claims{UserID: uuid.New(), MaxContentRating: "PG-13"}
)

type upcomingEnvelope struct {
	Data  upcomingResponse `json:"data"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (f *upcomingFixture) get(t *testing.T, claims *auth.Claims, query string) (int, upcomingEnvelope, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/upcoming"+query, nil)
	if claims != nil {
		req = req.WithContext(middleware.WithClaims(req.Context(), claims))
	}
	rec := httptest.NewRecorder()
	f.h.Get(rec, req)
	var env upcomingEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec.Code, env, rec.Body.String()
}

func itemTitles(items []UpcomingItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Title
		if it.Kind == upcomingKindMovie {
			out[i] += "/" + it.ReleaseType
		}
	}
	return out
}

// ── range validation ──────────────────────────────────────────────────────

func TestUpcoming_RequiresAuth(t *testing.T) {
	f := newUpcomingFixture()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/upcoming", nil)
	rec := httptest.NewRecorder()
	f.h.Get(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestUpcoming_RangeValidation(t *testing.T) {
	cases := []struct {
		query    string
		status   int
		from, to string
	}{
		// Defaults: today (UTC) through today+30.
		{"", 200, "2026-10-10", "2026-11-09"},
		{"?from=2026-10-01", 200, "2026-10-01", "2026-10-31"},
		{"?to=2026-10-20", 200, "2026-10-10", "2026-10-20"},
		{"?from=2026-10-01&to=2026-10-01", 200, "2026-10-01", "2026-10-01"},
		{"?from=2026-10-01&to=2026-12-02", 200, "2026-10-01", "2026-12-02"}, // exactly 62
		{"?from=2026-10-01&to=2026-12-03", 400, "", ""},                     // 63
		{"?from=2026-10-02&to=2026-10-01", 400, "", ""},
		{"?to=2026-10-09", 400, "", ""}, // before the default from
		{"?from=2026-10-1", 400, "", ""},
		{"?from=yesterday", 400, "", ""},
		{"?from=2026-10-01&to=2026-10-01T00:00:00Z", 400, "", ""},
	}
	for _, c := range cases {
		f := newUpcomingFixture()
		status, env, body := f.get(t, upcomingUser, c.query)
		if status != c.status {
			t.Errorf("%q: status = %d, want %d (%s)", c.query, status, c.status, body)
			continue
		}
		if c.status == 400 {
			if env.Error.Code != "VALIDATION" {
				t.Errorf("%q: code = %q, want VALIDATION", c.query, env.Error.Code)
			}
			continue
		}
		if env.Data.From != c.from || env.Data.To != c.to {
			t.Errorf("%q: window = %s..%s, want %s..%s", c.query, env.Data.From, env.Data.To, c.from, c.to)
		}
	}
}

func TestUpcoming_NoServicesIsEmptyNotNull(t *testing.T) {
	f := newUpcomingFixture(gen.ArrService{ID: uuid.New(), Name: "Off", Kind: "radarr", Enabled: false})
	status, _, body := f.get(t, upcomingUser, "")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if !strings.Contains(body, `"items":[]`) || !strings.Contains(body, `"services":[]`) {
		t.Errorf("body = %s, want empty items and services arrays (disabled service omitted)", body)
	}
}

// ── normalisation ─────────────────────────────────────────────────────────

func TestUpcoming_MovieReleaseDatesInWindowOnly(t *testing.T) {
	radarr := newFakeArr(t, `[{
		"id": 12, "title": "Brand New Day", "year": 2026, "tmdbId": 100,
		"overview": "Peter is back.", "certification": "PG-13", "hasFile": false,
		"inCinemas": "2026-09-15T00:00:00Z",
		"digitalRelease": "2026-10-05T00:00:00Z",
		"physicalRelease": "2026-10-31T00:00:00Z",
		"images": [{"coverType": "poster", "remoteUrl": "https://image.tmdb.org/p.jpg"}]
	},{
		"id": 13, "title": "Next Month", "year": 0, "tmdbId": 101,
		"inCinemas": "2026-11-01T00:00:00Z", "hasFile": false
	}]`)
	svc := arrSvc("Radarr 4K", "radarr", radarr.srv.URL)
	f := newUpcomingFixture(svc)

	status, env, body := f.get(t, upcomingAdmin, "?from=2026-10-01&to=2026-10-31")
	if status != 200 {
		t.Fatalf("status = %d: %s", status, body)
	}
	items := env.Data.Items
	if got := itemTitles(items); !slices.Equal(got, []string{"Brand New Day/digital", "Brand New Day/physical"}) {
		t.Fatalf("items = %v, want the digital + physical dates only", got)
	}
	d := items[0]
	if d.ID != "radarr:"+svc.ID.String()+":12:digital" || d.Kind != "movie" || !d.AllDay ||
		d.Date != "2026-10-05T00:00:00Z" || d.Subtitle != "" || d.Service != "Radarr 4K" ||
		d.Certification != "PG-13" || d.Overview != "Peter is back." || d.Network != "" {
		t.Errorf("digital item = %+v", d)
	}
	if d.Year == nil || *d.Year != 2026 {
		t.Errorf("year = %v, want 2026", d.Year)
	}
	if d.PosterURL == nil || *d.PosterURL != "https://image.tmdb.org/p.jpg" {
		t.Errorf("poster = %v", d.PosterURL)
	}
	// Clock is 2026-10-10: the digital date has passed without a file.
	if d.Status != "missing" || items[1].Status != "upcoming" {
		t.Errorf("status = %s / %s, want missing / upcoming", d.Status, items[1].Status)
	}
	if items[1].Date != "2026-10-31T00:00:00Z" {
		t.Errorf("physical date = %s, want the inclusive last day", items[1].Date)
	}
	if svcs := env.Data.Services; len(svcs) != 1 || !svcs[0].OK || svcs[0].Name != "Radarr 4K" || svcs[0].Kind != "radarr" {
		t.Errorf("services = %+v", svcs)
	}
}

func TestUpcoming_EpisodesSubtitleAndStatus(t *testing.T) {
	sonarr := newFakeArr(t, `[{
		"id": 501, "seasonNumber": 2, "episodeNumber": 5, "title": "The Long Night",
		"airDateUtc": "2026-10-03T01:00:00Z", "hasFile": true, "overview": "Winter.",
		"series": {"title": "The Show", "year": 2024, "tmdbId": 300, "certification": "TV-14",
		           "network": "HBO", "images": [{"coverType": "poster", "remoteUrl": "http://insecure/p.jpg"}]}
	},{
		"id": 502, "seasonNumber": 2, "episodeNumber": 6, "title": "",
		"airDateUtc": "2026-10-10T01:00:00Z", "hasFile": false,
		"series": {"title": "The Show", "certification": "TV-14"}
	},{
		"id": 503, "seasonNumber": 12, "episodeNumber": 104, "title": "  ",
		"airDateUtc": "2026-10-10T23:00:00Z", "hasFile": false,
		"series": {"title": "Another Show", "certification": "TV-14"}
	},{
		"id": 504, "seasonNumber": 1, "episodeNumber": 1, "title": "Too Late",
		"airDateUtc": "2026-11-01T00:00:00Z", "hasFile": false,
		"series": {"title": "Out Of Window"}
	}]`)
	svc := arrSvc("Sonarr", "sonarr", sonarr.srv.URL)
	f := newUpcomingFixture(svc)

	status, env, body := f.get(t, upcomingAdmin, "?from=2026-10-01&to=2026-10-31")
	if status != 200 {
		t.Fatalf("status = %d: %s", status, body)
	}
	items := env.Data.Items
	if len(items) != 3 {
		t.Fatalf("items = %v, want 3 (the Nov 1 airing is past the window)", itemTitles(items))
	}
	want := []struct{ title, subtitle, date, status string }{
		{"The Show", "S02E05 · The Long Night", "2026-10-03T01:00:00Z", "downloaded"},
		{"The Show", "S02E06", "2026-10-10T01:00:00Z", "missing"},
		{"Another Show", "S12E104", "2026-10-10T23:00:00Z", "upcoming"},
	}
	for i, w := range want {
		it := items[i]
		if it.Title != w.title || it.Subtitle != w.subtitle || it.Date != w.date || it.Status != w.status {
			t.Errorf("item %d = %q %q %s %s, want %q %q %s %s", i,
				it.Title, it.Subtitle, it.Date, it.Status, w.title, w.subtitle, w.date, w.status)
		}
		if it.Kind != "episode" || it.ReleaseType != "airing" || it.AllDay {
			t.Errorf("item %d kind/release/all_day = %s/%s/%v", i, it.Kind, it.ReleaseType, it.AllDay)
		}
	}
	first := items[0]
	if first.ID != "sonarr:"+svc.ID.String()+":501" || first.Network != "HBO" || first.Overview != "Winter." ||
		first.Year == nil || *first.Year != 2024 {
		t.Errorf("first episode = %+v", first)
	}
	if first.PosterURL != nil {
		t.Errorf("poster = %q, want null for a plain-http URL", *first.PosterURL)
	}
	if items[1].Year != nil {
		t.Errorf("year = %d, want null when the series has none", *items[1].Year)
	}
}

func TestUpcomingPoster_HTTPSOnly(t *testing.T) {
	cases := []struct {
		images string
		want   string // "" = null
	}{
		{`[{"coverType":"poster","remoteUrl":"https://image.tmdb.org/p.jpg"}]`, "https://image.tmdb.org/p.jpg"},
		{`[{"coverType":"poster","remoteUrl":"http://image.tmdb.org/p.jpg"}]`, ""},
		{`[{"coverType":"poster","remoteUrl":"//image.tmdb.org/p.jpg"}]`, ""},
		{`[{"coverType":"poster","remoteUrl":"javascript:alert(1)"}]`, ""},
		{`[{"coverType":"poster","remoteUrl":"https://user:pw@host/p.jpg"}]`, ""},
		{`[{"coverType":"poster","url":"/MediaCover/1/poster.jpg"}]`, ""},
		{`[{"coverType":"fanart","remoteUrl":"https://image.tmdb.org/f.jpg"}]`, ""},
		// First usable poster wins.
		{`[{"coverType":"poster","remoteUrl":"http://a/p.jpg"},{"coverType":"Poster","remoteUrl":"https://b/p.jpg"}]`, "https://b/p.jpg"},
		{`[]`, ""},
	}
	for _, c := range cases {
		var imgs []arr.MovieImage
		if err := json.Unmarshal([]byte(c.images), &imgs); err != nil {
			t.Fatal(err)
		}
		got := upcomingPoster(imgs)
		if (got == nil) != (c.want == "") || (got != nil && *got != c.want) {
			t.Errorf("%s: got %v, want %q", c.images, got, c.want)
		}
	}
}

func TestUpcoming_SortedByDateThenTitleAcrossServices(t *testing.T) {
	radarr := newFakeArr(t, `[
		{"id": 1, "title": "zeta", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 2, "title": "Alpha", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 3, "title": "Early", "digitalRelease": "2026-10-02T00:00:00Z"}
	]`)
	sonarr := newFakeArr(t, `[
		{"id": 9, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": "2026-10-04T20:00:00Z", "series": {"title": "Middle"}}
	]`)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", radarr.srv.URL), arrSvc("Sonarr", "sonarr", sonarr.srv.URL))

	_, env, _ := f.get(t, upcomingAdmin, "?from=2026-10-01&to=2026-10-31")
	got := itemTitles(env.Data.Items)
	want := []string{"Early/digital", "Middle", "Alpha/digital", "zeta/digital"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// ── cache ─────────────────────────────────────────────────────────────────

func TestUpcoming_CacheSharedAcrossUsersUntilExpiry(t *testing.T) {
	radarr := newFakeArr(t, `[{"id": 1, "title": "Film", "certification": "PG", "digitalRelease": "2026-10-20T00:00:00Z"}]`)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", radarr.srv.URL))

	q := "?from=2026-10-01&to=2026-10-31"
	f.get(t, upcomingAdmin, q)
	f.get(t, upcomingKid, q)
	f.get(t, upcomingUser, q)
	if n := radarr.hits.Load(); n != 1 {
		t.Fatalf("hits after 3 users = %d, want 1 (shared cache)", n)
	}
	// A different window is a different entry.
	f.get(t, upcomingUser, "?from=2026-10-01&to=2026-10-30")
	if n := radarr.hits.Load(); n != 2 {
		t.Fatalf("hits after a second window = %d, want 2", n)
	}

	*f.clock = f.clock.Add(upcomingCacheTTL - time.Second)
	f.get(t, upcomingUser, q)
	if n := radarr.hits.Load(); n != 2 {
		t.Fatalf("hits just before expiry = %d, want 2", n)
	}
	*f.clock = f.clock.Add(2 * time.Second)
	_, env, _ := f.get(t, upcomingUser, q)
	if n := radarr.hits.Load(); n != 3 {
		t.Fatalf("hits after expiry = %d, want 3", n)
	}
	if len(env.Data.Items) != 1 || env.Data.Items[0].Status != "upcoming" {
		t.Fatalf("items = %+v", env.Data.Items)
	}
}

func TestUpcoming_StatusRecomputedFromCachedSnapshot(t *testing.T) {
	// Clock is 12:00; the episode airs at 12:02. Three minutes later the
	// snapshot is still cached, but the status must move to missing.
	sonarr := newFakeArr(t, `[{"id": 1, "seasonNumber": 1, "episodeNumber": 1,
		"airDateUtc": "2026-10-10T12:02:00Z", "series": {"title": "Live"}}]`)
	f := newUpcomingFixture(arrSvc("Sonarr", "sonarr", sonarr.srv.URL))

	_, env, _ := f.get(t, upcomingAdmin, "")
	if env.Data.Items[0].Status != "upcoming" {
		t.Fatalf("status = %s, want upcoming", env.Data.Items[0].Status)
	}
	*f.clock = f.clock.Add(3 * time.Minute)
	_, env, _ = f.get(t, upcomingAdmin, "")
	if sonarr.hits.Load() != 1 {
		t.Fatalf("hits = %d, want the cached snapshot reused", sonarr.hits.Load())
	}
	if env.Data.Items[0].Status != "missing" {
		t.Errorf("status = %s, want missing once the air time passed", env.Data.Items[0].Status)
	}
}

func TestUpcoming_ConcurrentMissesShareOneFetch(t *testing.T) {
	var hits atomic.Int32
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-gate
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(srv.Close)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", srv.URL))

	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/upcoming", nil)
			req = req.WithContext(middleware.WithClaims(req.Context(), upcomingUser))
			rec := httptest.NewRecorder()
			f.h.Get(rec, req)
			codes <- rec.Code
		}()
	}
	time.Sleep(50 * time.Millisecond) // let the callers pile up on the flight
	close(gate)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("status = %d", code)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("hits = %d, want 1", n)
	}
}

func TestUpcomingCache_BoundedEvictsSoonestExpiry(t *testing.T) {
	c := newUpcomingCache(2)
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	c.put("a", &upcomingSnapshot{}, now, time.Minute)
	c.put("b", &upcomingSnapshot{}, now, 2*time.Minute)
	c.put("c", &upcomingSnapshot{}, now, 3*time.Minute)
	if len(c.entries) != 2 {
		t.Fatalf("size = %d, want 2", len(c.entries))
	}
	if _, ok := c.get("a", now); ok {
		t.Error("a should have been evicted (soonest expiry)")
	}
	// Expired entries go first, even if younger ones exist.
	c.put("d", &upcomingSnapshot{}, now.Add(150*time.Second), time.Minute)
	if _, ok := c.entries["b"]; ok {
		t.Error("expired b should have been swept")
	}
	if _, ok := c.entries["c"]; !ok {
		t.Error("live c should have survived")
	}
}

// ── service failures ──────────────────────────────────────────────────────

func TestUpcoming_PartialServiceFailure(t *testing.T) {
	badKey := newFakeArr(t, ``)
	badKey.status = http.StatusUnauthorized
	broken := newFakeArr(t, `{"not": "an array"}`)
	down := newFakeArr(t, `[]`)
	downURL := down.srv.URL
	down.srv.Close()
	sonarr := newFakeArr(t, `[{"id": 7, "seasonNumber": 1, "episodeNumber": 2, "airDateUtc": "2026-10-12T02:00:00Z",
		"series": {"title": "Still Works", "certification": "TV-PG"}}]`)

	f := newUpcomingFixture(
		arrSvc("Radarr", "radarr", badKey.srv.URL),
		arrSvc("Radarr Broken", "radarr", broken.srv.URL),
		arrSvc("Radarr Down", "radarr", downURL),
		arrSvc("Sonarr", "sonarr", sonarr.srv.URL),
	)
	status, env, body := f.get(t, upcomingUser, "?from=2026-10-01&to=2026-10-31")
	if status != 200 {
		t.Fatalf("status = %d: %s", status, body)
	}
	if got := itemTitles(env.Data.Items); !slices.Equal(got, []string{"Still Works"}) {
		t.Errorf("items = %v, want the healthy Sonarr's episode", got)
	}
	want := map[string]string{
		"Radarr":        "authentication failed",
		"Radarr Broken": "unexpected response",
		"Radarr Down":   "unreachable",
		"Sonarr":        "",
	}
	if len(env.Data.Services) != len(want) {
		t.Fatalf("services = %+v", env.Data.Services)
	}
	for _, s := range env.Data.Services {
		if s.Error != want[s.Name] || s.OK != (want[s.Name] == "") {
			t.Errorf("service %s = ok:%v error:%q, want error %q", s.Name, s.OK, s.Error, want[s.Name])
		}
	}
	// Nothing about where the services live may reach the client.
	for _, leak := range []string{"127.0.0.1", "http://", "key"} {
		if strings.Contains(body, leak) {
			t.Errorf("response leaks %q: %s", leak, body)
		}
	}

	// A degraded snapshot is cached only briefly.
	hits := sonarr.hits.Load()
	*f.clock = f.clock.Add(upcomingDegradedTTL + time.Second)
	f.get(t, upcomingUser, "?from=2026-10-01&to=2026-10-31")
	if sonarr.hits.Load() != hits+1 {
		t.Errorf("degraded snapshot served past %v", upcomingDegradedTTL)
	}
}

func TestUpcoming_SealedKeyWithoutEncryptorIsMisconfigured(t *testing.T) {
	radarr := newFakeArr(t, `[]`)
	svc := arrSvc("Radarr", "radarr", radarr.srv.URL)
	svc.ApiKey = "arrenc1:ciphertext"
	f := newUpcomingFixture(svc)
	_, env, _ := f.get(t, upcomingAdmin, "")
	if s := env.Data.Services; len(s) != 1 || s[0].OK || s[0].Error != "misconfigured" {
		t.Errorf("services = %+v, want misconfigured", s)
	}
	if radarr.hits.Load() != 0 {
		t.Error("ciphertext must never be sent upstream as a key")
	}
}

// ── per-user filtering ────────────────────────────────────────────────────

func TestUpcoming_ContentRatingCeiling(t *testing.T) {
	radarr := newFakeArr(t, `[
		{"id": 1, "title": "Kids", "certification": "PG", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 2, "title": "Teen", "certification": "PG-13", "digitalRelease": "2026-10-06T00:00:00Z"},
		{"id": 3, "title": "Adult", "certification": "R", "digitalRelease": "2026-10-07T00:00:00Z"},
		{"id": 4, "title": "Unrated", "certification": "", "digitalRelease": "2026-10-08T00:00:00Z"}
	]`)
	sonarr := newFakeArr(t, `[
		{"id": 5, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": "2026-10-09T01:00:00Z",
		 "series": {"title": "Mature Show", "certification": "TV-MA"}},
		{"id": 6, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": "2026-10-09T02:00:00Z",
		 "series": {"title": "Family Show", "certification": "TV-PG"}}
	]`)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", radarr.srv.URL), arrSvc("Sonarr", "sonarr", sonarr.srv.URL))
	q := "?from=2026-10-01&to=2026-10-31"

	all := []string{"Kids/digital", "Teen/digital", "Adult/digital", "Unrated/digital", "Mature Show", "Family Show"}
	for _, c := range []struct {
		name   string
		claims *auth.Claims
		want   []string
	}{
		{"admin", upcomingAdmin, all},
		{"unrestricted user", upcomingUser, all},
		{"PG-13 profile", upcomingKid, []string{"Kids/digital", "Teen/digital", "Family Show"}},
		// An admin's own ceiling (if one were set) doesn't apply: admins see everything.
		{"admin with ceiling", &auth.Claims{UserID: uuid.New(), IsAdmin: true, MaxContentRating: "G"}, all},
	} {
		_, env, _ := f.get(t, c.claims, q)
		if got := itemTitles(env.Data.Items); !slices.Equal(got, c.want) {
			t.Errorf("%s: items = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestUpcoming_LibraryACLViaPathMapping(t *testing.T) {
	radarr := newFakeArr(t, `[
		{"id": 1, "title": "Open", "path": "/data/media/movies/Open (2026)", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 2, "title": "Private", "path": "/data/media/private/Private (2026)", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 3, "title": "Sibling", "path": "/data/media/privateish/Sibling (2026)", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 4, "title": "Unmapped", "path": "/elsewhere/Unmapped (2026)", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 5, "title": "Windows", "path": "D:\\Media\\private\\Win (2026)", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 6, "title": "Nested", "path": "/data/media/private/public-shelf/Nested (2026)", "digitalRelease": "2026-10-05T00:00:00Z"}
	]`)
	sonarr := newFakeArr(t, `[
		{"id": 7, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": "2026-10-06T01:00:00Z",
		 "series": {"title": "Private Show", "path": "/tv-remote/Private Show"}}
	]`)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", radarr.srv.URL), arrSvc("Sonarr", "sonarr", sonarr.srv.URL))

	movies, private, shelf, tv := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.libs.libs = []library.Library{
		{ID: movies, Paths: []string{"/mnt/media/movies"}},
		{ID: private, Paths: []string{"/mnt/media/private/"}},
		// A library nested inside a hidden one: the more specific root wins.
		{ID: shelf, Paths: []string{"/mnt/media/private/public-shelf"}},
		{ID: tv, Paths: []string{"/srv/tv/private"}},
	}
	f.access.allowed = []uuid.UUID{movies, shelf}
	f.paths["/data/media"] = "/mnt/media"
	f.paths["/data/media/movies"] = "/mnt/media/movies" // longest prefix wins; same result
	f.paths[`D:\Media`] = "/mnt/media"
	f.paths["/tv-remote/"] = "/srv/tv/private"
	q := "?from=2026-10-01&to=2026-10-31"

	_, env, _ := f.get(t, upcomingUser, q)
	got := itemTitles(env.Data.Items)
	slices.Sort(got)
	want := []string{"Nested/digital", "Open/digital", "Sibling/digital", "Unmapped/digital"}
	if !slices.Equal(got, want) {
		t.Errorf("user items = %v, want %v", got, want)
	}

	_, env, _ = f.get(t, upcomingAdmin, q)
	if len(env.Data.Items) != 7 {
		t.Errorf("admin items = %v, want all 7", itemTitles(env.Data.Items))
	}

	// With every library granted, nothing is hidden.
	f.access.allowed = []uuid.UUID{movies, private, shelf, tv}
	_, env, _ = f.get(t, upcomingUser, q)
	if len(env.Data.Items) != 7 {
		t.Errorf("fully granted user items = %v, want all 7", itemTitles(env.Data.Items))
	}
}

func TestMapUpcomingPath(t *testing.T) {
	m := map[string]string{
		"/data":             "/mnt/a",
		"/data/media/":      "/mnt/b",
		`C:\Downloads\Film`: "/mnt/c",
		"":                  "/ignored",
	}
	cases := map[string]string{
		"/data/media/Film (2026)":     "/mnt/b/Film (2026)",
		"/data/other/Film":            "/mnt/a/other/Film",
		"/data":                       "/mnt/a",
		"/datastore/Film":             "/datastore/Film", // not a path boundary
		`C:\Downloads\Film\X (2026)\`: "/mnt/c/X (2026)",
		"/unmapped//double/slash/":    "/unmapped/double/slash",
		"":                            "",
		"  ":                          "",
	}
	for in, want := range cases {
		if got := mapUpcomingPath(in, m); got != want {
			t.Errorf("mapUpcomingPath(%q) = %q, want %q", in, got, want)
		}
	}
	if got := mapUpcomingPath("/x/y", nil); got != "/x/y" {
		t.Errorf("nil mappings: %q", got)
	}
}

// ── item linking ──────────────────────────────────────────────────────────

func TestUpcoming_ItemLinkingRespectsAccess(t *testing.T) {
	radarr := newFakeArr(t, `[
		{"id": 1, "title": "Owned", "tmdbId": 100, "certification": "PG", "digitalRelease": "2026-10-05T00:00:00Z"},
		{"id": 2, "title": "Owned", "tmdbId": 100, "certification": "PG", "physicalRelease": "2026-10-06T00:00:00Z"},
		{"id": 3, "title": "Hidden Copy", "tmdbId": 200, "certification": "PG", "digitalRelease": "2026-10-07T00:00:00Z"},
		{"id": 4, "title": "Not Owned", "tmdbId": 999, "certification": "PG", "digitalRelease": "2026-10-08T00:00:00Z"}
	]`)
	sonarr := newFakeArr(t, `[
		{"id": 5, "seasonNumber": 3, "episodeNumber": 1, "airDateUtc": "2026-10-09T01:00:00Z",
		 "series": {"title": "Linked Show", "tmdbId": 300, "certification": "TV-PG"}},
		{"id": 6, "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": "2026-10-09T02:00:00Z",
		 "series": {"title": "Over Ceiling In Library", "tmdbId": 400, "certification": "TV-PG"}}
	]`)
	f := newUpcomingFixture(arrSvc("Radarr", "radarr", radarr.srv.URL), arrSvc("Sonarr", "sonarr", sonarr.srv.URL))

	open, locked := uuid.New(), uuid.New()
	owned, hidden, show, mature := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.access.allowed = []uuid.UUID{open}
	f.libs.libs = []library.Library{{ID: open, Paths: []string{"/lib/open"}}, {ID: locked, Paths: []string{"/lib/locked"}}}
	f.db.items = []upcomingLinkRow{
		{id: owned, libraryID: open, itemType: "movie", tmdbID: 100, rating: "PG"},
		{id: hidden, libraryID: locked, itemType: "movie", tmdbID: 200, rating: "PG"},
		{id: show, libraryID: open, itemType: "show", tmdbID: 300, rating: "TV-PG"},
		// OnScreen's copy is rated higher than Sonarr's certification says.
		{id: mature, libraryID: open, itemType: "show", tmdbID: 400, rating: "TV-MA"},
	}
	q := "?from=2026-10-01&to=2026-10-31"

	linked := func(items []UpcomingItem) map[string]string {
		out := map[string]string{}
		for _, it := range items {
			key := it.Title + "/" + it.ReleaseType
			if it.ItemID != nil {
				out[key] = *it.ItemID
			} else {
				out[key] = ""
			}
		}
		return out
	}

	f.db.lookups = nil
	_, env, _ := f.get(t, upcomingKid, q)
	got := linked(env.Data.Items)
	want := map[string]string{
		"Owned/digital":                  owned.String(),
		"Owned/physical":                 owned.String(),
		"Hidden Copy/digital":            "",
		"Not Owned/digital":              "",
		"Linked Show/airing":             show.String(),
		"Over Ceiling In Library/airing": "",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("PG-13 profile %s item_id = %q, want %q", k, got[k], v)
		}
	}
	if len(f.db.lookups) != 2 {
		t.Errorf("lookups = %d, want one batched lookup per kind", len(f.db.lookups))
	}
	for _, l := range f.db.lookups {
		if l.LibraryIds == nil || l.MaxRatingRank == nil {
			t.Errorf("non-admin lookup not scoped: %+v", l)
		}
		if l.Type == "movie" && !slices.Equal(l.TmdbIds, []int32{100, 200, 999}) {
			t.Errorf("movie tmdb ids = %v, want deduplicated [100 200 999]", l.TmdbIds)
		}
	}

	_, env, _ = f.get(t, upcomingAdmin, q)
	got = linked(env.Data.Items)
	if got["Hidden Copy/digital"] != hidden.String() || got["Over Ceiling In Library/airing"] != mature.String() {
		t.Errorf("admin links = %v, want every match linked", got)
	}

	// A user with no library grants at all gets no links and no lookup.
	f.access.allowed = nil
	f.db.lookups = nil
	_, env, _ = f.get(t, upcomingUser, q)
	for _, it := range env.Data.Items {
		if it.ItemID != nil {
			t.Errorf("%s linked for a user with no grants", it.Title)
		}
	}
	if len(f.db.lookups) != 0 {
		t.Errorf("lookups = %d, want none when the caller has no libraries", len(f.db.lookups))
	}
}
