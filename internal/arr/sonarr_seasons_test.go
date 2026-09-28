package arr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

// seasonsSonarr is a scripted Sonarr for MonitorSeasons: it serves one series
// resource and records the PUT body and every command.
type seasonsSonarr struct {
	t      *testing.T
	series string

	mu       sync.Mutex
	put      map[string]any
	putPath  string
	commands []map[string]any
	calls    []string
	cmdFail  bool
}

func newSeasonsSonarr(t *testing.T, series string) (*seasonsSonarr, *Client) {
	s := &seasonsSonarr{t: t, series: series}
	srv := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(srv.Close)
	return s, New(srv.URL, "k")
}

func (s *seasonsSonarr) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") != "k" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/42":
		_, _ = io.WriteString(w, s.series)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v3/series/42":
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &s.put); err != nil {
			s.t.Errorf("PUT body: %v", err)
		}
		s.putPath = r.URL.Path
		_, _ = w.Write(raw)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
		if s.cmdFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		s.commands = append(s.commands, body)
		_, _ = io.WriteString(w, `{"id":9}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// A series Sonarr already manages, with S1 monitored and complete, S2/S3
// unmonitored, specials unmonitored, and fields this client doesn't model.
const existingSeries = `{
  "id": 42, "title": "Show", "tvdbId": 100, "monitored": true,
  "path": "/tv/Show", "qualityProfileId": 7, "tags": [3], "seasonFolder": true,
  "alternateTitles": [{"title": "Alt"}], "addOptions": null,
  "seasons": [
    {"seasonNumber": 0, "monitored": false, "statistics": {"episodeFileCount": 0, "episodeCount": 0, "totalEpisodeCount": 4}},
    {"seasonNumber": 1, "monitored": true, "statistics": {"episodeFileCount": 10, "episodeCount": 10, "totalEpisodeCount": 10}},
    {"seasonNumber": 2, "monitored": false, "statistics": {"episodeFileCount": 0, "episodeCount": 0, "totalEpisodeCount": 8}},
    {"seasonNumber": 3, "monitored": false, "statistics": {"episodeFileCount": 0, "episodeCount": 0, "totalEpisodeCount": 8}}
  ]
}`

func seasonMonitored(t *testing.T, put map[string]any) map[int]bool {
	t.Helper()
	out := map[int]bool{}
	list, _ := put["seasons"].([]any)
	for _, raw := range list {
		se := raw.(map[string]any)
		out[int(se["seasonNumber"].(float64))] = se["monitored"].(bool)
	}
	return out
}

func TestMonitorSeasons_MonitorsRequestedAndSearchesThem(t *testing.T) {
	s, c := newSeasonsSonarr(t, existingSeries)
	res, err := c.MonitorSeasons(context.Background(), 42, []int{2, 3, 9})
	if err != nil {
		t.Fatalf("MonitorSeasons: %v", err)
	}
	if !reflect.DeepEqual(res.Monitored, []int{2, 3}) || !reflect.DeepEqual(res.Searched, []int{2, 3}) {
		t.Errorf("monitored=%v searched=%v, want [2 3] both", res.Monitored, res.Searched)
	}
	if !reflect.DeepEqual(res.Missing, []int{9}) {
		t.Errorf("missing = %v, want [9]", res.Missing)
	}
	if s.putPath != "/api/v3/series/42" {
		t.Fatalf("PUT path = %q", s.putPath)
	}
	got := seasonMonitored(t, s.put)
	want := map[int]bool{0: false, 1: true, 2: true, 3: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("season monitoring after PUT = %v, want %v", got, want)
	}
	// Everything else goes back exactly as Sonarr sent it.
	if s.put["path"] != "/tv/Show" || s.put["qualityProfileId"] != float64(7) || s.put["seasonFolder"] != true {
		t.Errorf("PUT dropped series fields: %v", s.put)
	}
	if tags, _ := s.put["tags"].([]any); len(tags) != 1 || tags[0] != float64(3) {
		t.Errorf("tags = %v, want [3]", s.put["tags"])
	}
	if _, ok := s.put["alternateTitles"]; !ok {
		t.Error("PUT dropped an unmodelled field (alternateTitles)")
	}
	// Season statistics are echoed back untouched.
	list := s.put["seasons"].([]any)
	st := list[1].(map[string]any)["statistics"].(map[string]any)
	if st["episodeFileCount"] != float64(10) {
		t.Errorf("season 1 statistics changed: %v", st)
	}
	wantCmds := []map[string]any{
		{"name": "SeasonSearch", "seriesId": float64(42), "seasonNumber": float64(2)},
		{"name": "SeasonSearch", "seriesId": float64(42), "seasonNumber": float64(3)},
	}
	if !reflect.DeepEqual(s.commands, wantCmds) {
		t.Errorf("commands = %v, want %v", s.commands, wantCmds)
	}
}

func TestMonitorSeasons_AllSeasonsSkipsSpecials(t *testing.T) {
	s, c := newSeasonsSonarr(t, existingSeries)
	res, err := c.MonitorSeasons(context.Background(), 42, nil)
	if err != nil {
		t.Fatalf("MonitorSeasons: %v", err)
	}
	if !reflect.DeepEqual(res.Monitored, []int{2, 3}) {
		t.Errorf("monitored = %v, want [2 3]", res.Monitored)
	}
	got := seasonMonitored(t, s.put)
	if got[0] {
		t.Error("specials were monitored by an all-seasons request")
	}
	if len(s.commands) != 2 {
		t.Errorf("commands = %v, want two season searches", s.commands)
	}
}

func TestMonitorSeasons_AlreadyMonitoredIsANoOp(t *testing.T) {
	s, c := newSeasonsSonarr(t, existingSeries)
	res, err := c.MonitorSeasons(context.Background(), 42, []int{1})
	if err != nil {
		t.Fatalf("MonitorSeasons: %v", err)
	}
	if len(res.Monitored) != 0 || len(res.Searched) != 0 {
		t.Errorf("res = %+v, want nothing changed", res)
	}
	for _, c := range s.calls {
		if c != "GET /api/v3/series/42" {
			t.Errorf("unexpected call %s (nothing needed to change)", c)
		}
	}
}

func TestMonitorSeasons_UnmonitoredSeriesIsSwitchedOnAndSearched(t *testing.T) {
	series := `{"id": 42, "monitored": false, "seasons": [
	  {"seasonNumber": 1, "monitored": true},
	  {"seasonNumber": 2, "monitored": false}]}`
	s, c := newSeasonsSonarr(t, series)
	res, err := c.MonitorSeasons(context.Background(), 42, []int{1, 2})
	if err != nil {
		t.Fatalf("MonitorSeasons: %v", err)
	}
	if s.put["monitored"] != true {
		t.Errorf("series monitored = %v, want true", s.put["monitored"])
	}
	if !reflect.DeepEqual(res.Monitored, []int{2}) || !reflect.DeepEqual(res.Searched, []int{1, 2}) {
		t.Errorf("monitored=%v searched=%v, want [2] / [1 2]", res.Monitored, res.Searched)
	}
}

func TestMonitorSeasons_SearchFailureKeepsMonitoring(t *testing.T) {
	s, c := newSeasonsSonarr(t, existingSeries)
	s.cmdFail = true
	res, err := c.MonitorSeasons(context.Background(), 42, []int{2})
	if err != nil {
		t.Fatalf("MonitorSeasons: %v (a failed search must not fail the monitor)", err)
	}
	if res.SearchErr == nil || len(res.Searched) != 0 || !reflect.DeepEqual(res.Monitored, []int{2}) {
		t.Errorf("res = %+v, want season 2 monitored with a search error", res)
	}
}

func TestMonitorSeasons_NotFound(t *testing.T) {
	_, c := newSeasonsSonarr(t, existingSeries)
	if _, err := c.MonitorSeasons(context.Background(), 7, []int{1}); err != ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSeriesSeasonState_Complete(t *testing.T) {
	cases := []struct {
		name  string
		st    *SeasonStatistics
		aired bool
		done  bool
	}{
		{"no statistics", nil, false, false},
		{"nothing aired", &SeasonStatistics{EpisodeCount: 0, TotalEpisodeCount: 8}, false, false},
		{"partial", &SeasonStatistics{EpisodeFileCount: 3, EpisodeCount: 8}, true, false},
		{"complete", &SeasonStatistics{EpisodeFileCount: 8, EpisodeCount: 8}, true, true},
		{"airing season, aired ones done", &SeasonStatistics{EpisodeFileCount: 4, EpisodeCount: 4, TotalEpisodeCount: 10}, true, true},
	}
	for _, tc := range cases {
		s := SeriesSeasonState{SeasonNumber: 1, Statistics: tc.st}
		if s.Aired() != tc.aired || s.Complete() != tc.done {
			t.Errorf("%s: aired=%v complete=%v, want %v/%v", tc.name, s.Aired(), s.Complete(), tc.aired, tc.done)
		}
	}
}

func TestSeriesByID_DecodesSeasons(t *testing.T) {
	_, c := newSeasonsSonarr(t, existingSeries)
	s, err := c.SeriesByID(context.Background(), 42)
	if err != nil {
		t.Fatalf("SeriesByID: %v", err)
	}
	se, ok := s.Season(1)
	if !ok || !se.Complete() {
		t.Errorf("season 1 = %+v ok=%v, want complete", se, ok)
	}
	if _, ok := s.Season(5); ok {
		t.Error("season 5 should not exist")
	}
}
