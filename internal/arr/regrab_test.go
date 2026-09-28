package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeArr is a scripted Radarr/Sonarr: routes map "METHOD /path" to a JSON
// body (or a status code), and every command / failed-history POST is
// recorded for assertions.
type fakeArr struct {
	t      *testing.T
	routes map[string]string
	status map[string]int

	mu       sync.Mutex
	commands []map[string]any
	failed   []string
	queries  map[string]string
}

func newFakeArr(t *testing.T, routes map[string]string) (*fakeArr, *httptest.Server) {
	f := &fakeArr{t: t, routes: routes, status: map[string]int{}, queries: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeArr) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") != "k" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.queries[key] = r.URL.RawQuery
	f.mu.Unlock()
	if code, ok := f.status[key]; ok {
		w.WriteHeader(code)
		return
	}
	switch {
	case key == "POST /api/v3/command":
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			f.t.Errorf("command body: %v", err)
		}
		f.mu.Lock()
		f.commands = append(f.commands, body)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"id":77,"name":"x","status":"queued"}`)
		return
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/v3/history/failed/"):
		f.mu.Lock()
		f.failed = append(f.failed, strings.TrimPrefix(r.URL.Path, "/api/v3/history/failed/"))
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	body, ok := f.routes[key]
	if !ok {
		f.t.Errorf("unexpected request %s?%s", key, r.URL.RawQuery)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, _ = io.WriteString(w, body)
}

func jsonRoundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRegrab_Movie_SearchOnly(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{
		// A Radarr that ignored the filter would return the whole library;
		// the exact tmdbId row must be the one picked.
		"GET /api/v3/movie": `[{"id":3,"title":"Other","tmdbId":1},{"id":9,"title":"The Matrix","tmdbId":603,"hasFile":true}]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	if got := f.queries["GET /api/v3/movie"]; got != "tmdbId=603" {
		t.Errorf("movie query = %q, want tmdbId=603", got)
	}
	if res.Command != CommandMoviesSearch || res.ArrID != 9 || res.CommandID != 77 || res.Blocklisted ||
		res.HasFile == nil || !*res.HasFile || res.Title != "The Matrix" {
		t.Errorf("result = %+v", res)
	}
	want := []map[string]any{{"name": "MoviesSearch", "movieIds": []any{float64(9)}}}
	if !reflect.DeepEqual(f.commands, want) {
		t.Errorf("commands = %v, want %v", f.commands, want)
	}
	if len(f.failed) != 0 {
		t.Errorf("search-only re-grab marked history failed: %v", f.failed)
	}
}

func TestRegrab_Movie_BlocklistsImportedGrab(t *testing.T) {
	// Two grabs: the newer one (id 12) is still downloading; the file on disk
	// came from the older grab (id 10, downloadId A) — that's the release to
	// blocklist.
	f, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/movie": `[{"id":9,"title":"M","tmdbId":603}]`,
		"GET /api/v3/history/movie": `[
			{"id":10,"eventType":"grabbed","date":"2026-09-01T10:00:00Z","sourceTitle":"M.2026.BAD","downloadId":"A","movieId":9},
			{"id":11,"eventType":"downloadFolderImported","date":"2026-09-01T11:00:00Z","downloadId":"A","movieId":9},
			{"id":12,"eventType":"grabbed","date":"2026-09-05T10:00:00Z","sourceTitle":"M.2026.NEW","downloadId":"B","movieId":9}
		]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, true)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	if got := f.queries["GET /api/v3/history/movie"]; got != "movieId=9" {
		t.Errorf("history query = %q", got)
	}
	if !reflect.DeepEqual(f.failed, []string{"10"}) {
		t.Errorf("failed = %v, want [10]", f.failed)
	}
	if !res.Blocklisted || res.BlocklistedRelease != "M.2026.BAD" {
		t.Errorf("result = %+v", res)
	}
	if len(f.commands) != 1 || f.commands[0]["name"] != "MoviesSearch" {
		t.Errorf("commands = %v, want one MoviesSearch after the blocklist", f.commands)
	}
}

func TestRegrab_Movie_BlocklistWithoutHistoryStillSearches(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/movie":         `[{"id":9,"title":"M","tmdbId":603}]`,
		"GET /api/v3/history/movie": `[{"id":4,"eventType":"movieFileRenamed","date":"2026-09-01T10:00:00Z"}]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, true)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	if res.Blocklisted || len(f.failed) != 0 || len(f.commands) != 1 {
		t.Errorf("result = %+v failed=%v commands=%v", res, f.failed, f.commands)
	}
}

func TestRegrab_Movie_NotManaged(t *testing.T) {
	_, srv := newFakeArr(t, map[string]string{"GET /api/v3/movie": `[]`})
	_, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrNotManaged", err)
	}
	// No TMDB id at all: nothing to look up.
	_, err = New("http://127.0.0.1:1", "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("no tmdb id: err = %v, want ErrNotManaged", err)
	}
}

func TestRegrab_UpstreamErrorsAreNotNotManaged(t *testing.T) {
	f, srv := newFakeArr(t, nil)
	f.status["GET /api/v3/movie"] = http.StatusInternalServerError
	_, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if err == nil || errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want an upstream error", err)
	}
	// Bad key → ErrUnauthorized, still not "not managed".
	_, err = newTestClient(srv, "wrong").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestRegrab_CommandFailureIsActionError(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{"GET /api/v3/movie": `[{"id":9,"title":"M","tmdbId":603}]`})
	f.status["POST /api/v3/command"] = http.StatusBadGateway
	_, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if !errors.Is(err, ErrRegrabAction) || errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrRegrabAction", err)
	}
	// A lookup failure is not an action error: the caller may try another
	// instance.
	f.status["GET /api/v3/movie"] = http.StatusInternalServerError
	_, err = newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabMovie, TMDBID: 603}, false)
	if errors.Is(err, ErrRegrabAction) {
		t.Errorf("lookup failure reported as action error: %v", err)
	}
}

func TestRegrab_Episode_ByTVDB(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series": `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/episode": `[
			{"id":100,"seriesId":5,"seasonNumber":2,"episodeNumber":1,"hasFile":true},
			{"id":101,"seriesId":5,"seasonNumber":2,"episodeNumber":2,"hasFile":false}
		]`,
		"GET /api/v3/history/series": `[
			{"id":50,"eventType":"grabbed","date":"2026-09-02T10:00:00Z","sourceTitle":"S02E01.BAD","episodeId":100},
			{"id":51,"eventType":"grabbed","date":"2026-09-03T10:00:00Z","sourceTitle":"S02E02","episodeId":101}
		]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(),
		RegrabTarget{Scope: RegrabEpisode, TVDBID: 81189, SeasonNumber: 2, EpisodeNumber: 1}, true)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	if got := f.queries["GET /api/v3/series"]; got != "tvdbId=81189" {
		t.Errorf("series query = %q", got)
	}
	if got := f.queries["GET /api/v3/episode"]; got != "seasonNumber=2&seriesId=5" {
		t.Errorf("episode query = %q", got)
	}
	if got := f.queries["GET /api/v3/history/series"]; got != "seasonNumber=2&seriesId=5" {
		t.Errorf("history query = %q", got)
	}
	// Only the reported episode's grab is blocklisted, not the newer one.
	if !reflect.DeepEqual(f.failed, []string{"50"}) {
		t.Errorf("failed = %v, want [50]", f.failed)
	}
	want := []map[string]any{{"name": "EpisodeSearch", "episodeIds": []any{float64(100)}}}
	if !reflect.DeepEqual(f.commands, want) {
		t.Errorf("commands = %v, want %v", f.commands, want)
	}
	if res.Command != CommandEpisodeSearch || res.HasFile == nil || !*res.HasFile || res.ArrID != 5 {
		t.Errorf("result = %+v", res)
	}
}

func TestRegrab_Episode_MissingEpisodeIsNotManaged(t *testing.T) {
	_, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series":  `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/episode": `[{"id":100,"seriesId":5,"seasonNumber":2,"episodeNumber":1}]`,
	})
	_, err := newTestClient(srv, "k").Regrab(context.Background(),
		RegrabTarget{Scope: RegrabEpisode, TVDBID: 81189, SeasonNumber: 2, EpisodeNumber: 9}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrNotManaged", err)
	}
}

func TestRegrab_Season_IncludesSpecialsSeasonZero(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series":  `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/episode": `[{"id":1,"seriesId":5,"seasonNumber":0,"episodeNumber":1}]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(),
		RegrabTarget{Scope: RegrabSeason, TVDBID: 81189, SeasonNumber: 0}, false)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	// seasonNumber 0 must be sent, not dropped as a zero value.
	want := []map[string]any{{"name": "SeasonSearch", "seriesId": float64(5), "seasonNumber": float64(0)}}
	if !reflect.DeepEqual(f.commands, want) {
		t.Errorf("commands = %v, want %v", f.commands, want)
	}
	if res.HasFile != nil {
		t.Errorf("season re-grab reported has_file = %v", *res.HasFile)
	}
	if m := jsonRoundTrip(t, res); m["has_file"] != nil {
		t.Errorf("has_file must be omitted for a season: %v", m)
	}
}

func TestRegrab_Season_UnknownSeasonIsNotManaged(t *testing.T) {
	_, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series":  `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/episode": `[]`,
	})
	_, err := newTestClient(srv, "k").Regrab(context.Background(),
		RegrabTarget{Scope: RegrabSeason, TVDBID: 81189, SeasonNumber: 7}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrNotManaged", err)
	}
}

func TestRegrab_Series_ByTMDBViaLookup(t *testing.T) {
	f, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series/lookup": `[{"title":"Show","tvdbId":81189,"tmdbId":1399,"year":2011,"titleSlug":"show"}]`,
		"GET /api/v3/series":        `[{"id":5,"title":"Show","tvdbId":81189}]`,
		"GET /api/v3/history/series": `[
			{"id":7,"eventType":"grabbed","date":"2026-09-02T10:00:00Z","sourceTitle":"Show.S01","episodeId":1}
		]`,
	})
	res, err := newTestClient(srv, "k").Regrab(context.Background(),
		RegrabTarget{Scope: RegrabSeries, TMDBID: 1399}, true)
	if err != nil {
		t.Fatalf("Regrab: %v", err)
	}
	if got := f.queries["GET /api/v3/series/lookup"]; got != "term=tmdb%3A1399" {
		t.Errorf("lookup query = %q", got)
	}
	if got := f.queries["GET /api/v3/history/series"]; got != "seriesId=5" {
		t.Errorf("history query = %q, want the whole series", got)
	}
	want := []map[string]any{{"name": "SeriesSearch", "seriesId": float64(5)}}
	if !reflect.DeepEqual(f.commands, want) {
		t.Errorf("commands = %v, want %v", f.commands, want)
	}
	if !res.Blocklisted || !reflect.DeepEqual(f.failed, []string{"7"}) {
		t.Errorf("result = %+v failed = %v", res, f.failed)
	}
}

func TestRegrab_Series_LookupMismatchIsNotManaged(t *testing.T) {
	// The lookup falls back to its first result; a non-exact TMDB match must
	// not be trusted to name the series.
	_, srv := newFakeArr(t, map[string]string{
		"GET /api/v3/series/lookup": `[{"title":"Other","tvdbId":1,"tmdbId":2,"titleSlug":"other"}]`,
	})
	_, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabSeries, TMDBID: 1399}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrNotManaged", err)
	}
}

func TestRegrab_Series_NotInLibrary(t *testing.T) {
	_, srv := newFakeArr(t, map[string]string{"GET /api/v3/series": `[]`})
	_, err := newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabSeries, TVDBID: 81189}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want ErrNotManaged", err)
	}
	_, err = newTestClient(srv, "k").Regrab(context.Background(), RegrabTarget{Scope: RegrabSeries}, false)
	if !errors.Is(err, ErrNotManaged) {
		t.Errorf("no ids: err = %v, want ErrNotManaged", err)
	}
}

func TestRegrab_UnknownScope(t *testing.T) {
	_, err := New("http://127.0.0.1:1", "k").Regrab(context.Background(), RegrabTarget{Scope: "album"}, false)
	if err == nil || errors.Is(err, ErrNotManaged) {
		t.Errorf("err = %v, want an unknown-scope error", err)
	}
}

func TestPickGrabToBlocklist(t *testing.T) {
	ts := func(s string) Timestamp {
		var v Timestamp
		_ = v.UnmarshalJSON([]byte(`"` + s + `"`))
		return v
	}
	hist := []regrabHistoryRecord{
		{ID: 1, EventType: historyEventGrabbed, Date: ts("2026-09-01T00:00:00Z"), DownloadID: "A"},
		{ID: 2, EventType: historyEventGrabbed, Date: ts("2026-09-02T00:00:00Z"), DownloadID: "B"},
	}
	// No import: newest grab.
	if g := pickGrabToBlocklist(hist, nil); g == nil || g.ID != 2 {
		t.Errorf("no import: got %+v, want id 2", g)
	}
	// Import of A after B was grabbed: A is on disk.
	withImport := append(hist, regrabHistoryRecord{ID: 3, EventType: historyEventImported, Date: ts("2026-09-03T00:00:00Z"), DownloadID: "A"})
	if g := pickGrabToBlocklist(withImport, nil); g == nil || g.ID != 1 {
		t.Errorf("import of A: got %+v, want id 1", g)
	}
	// Import whose grab was pruned: fall back to the newest grab.
	orphan := append(hist, regrabHistoryRecord{ID: 4, EventType: historyEventImported, Date: ts("2026-09-03T00:00:00Z"), DownloadID: "Z"})
	if g := pickGrabToBlocklist(orphan, nil); g == nil || g.ID != 2 {
		t.Errorf("orphan import: got %+v, want id 2", g)
	}
	if g := pickGrabToBlocklist(nil, nil); g != nil {
		t.Errorf("empty history: got %+v", g)
	}
	if g := pickGrabToBlocklist(hist, func(regrabHistoryRecord) bool { return false }); g != nil {
		t.Errorf("filtered out: got %+v", g)
	}
}
