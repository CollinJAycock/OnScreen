package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLookupSeriesByTVDB_ExactIDMatchWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("term"); got != "tvdb:121361" {
			t.Errorf("term = %q, want \"tvdb:121361\"", got)
		}
		_, _ = io.WriteString(w, `[
			{"title":"Game of Thrones (US)","tvdbId":999999,"year":2010},
			{"title":"Game of Thrones","tvdbId":121361,"year":2011,"titleSlug":"got"}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LookupSeriesByTVDB(context.Background(), 121361)
	if err != nil {
		t.Fatalf("LookupSeriesByTVDB: %v", err)
	}
	if got.TVDBID != 121361 {
		t.Errorf("got %+v, want exact tvdb:121361 match", got)
	}
}

func TestLookupSeriesByTMDB_RoutesByTMDBID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
			{"title":"Wrong","tvdbId":1,"tmdbId":11},
			{"title":"Right","tvdbId":2,"tmdbId":42}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LookupSeriesByTMDB(context.Background(), 42)
	if err != nil {
		t.Fatalf("LookupSeriesByTMDB: %v", err)
	}
	if got.TMDBID != 42 {
		t.Errorf("got %+v, want tmdbId 42", got)
	}
}

func TestLookupSeriesByTitle_AcceptsAnyResult(t *testing.T) {
	// Title fallback uses match=>true, so the first result wins.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
			{"title":"Severance","tvdbId":371980,"year":2022}
		]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LookupSeriesByTitle(context.Background(), "Severance")
	if err != nil {
		t.Fatalf("LookupSeriesByTitle: %v", err)
	}
	if got.Title != "Severance" {
		t.Errorf("got %+v", got)
	}
}

func TestLanguageProfiles_404TreatedAsEmpty(t *testing.T) {
	// Sonarr v4 removed /api/v3/languageprofile. The client must NOT
	// surface ErrNotFound here — return an empty slice so the caller
	// can use the "no language profiles needed on v4" branch.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LanguageProfiles(context.Background())
	if err != nil {
		t.Errorf("got %v, want nil (404 must degrade silently for v4 compat)", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d profiles, want 0", len(got))
	}
}

func TestLanguageProfiles_DecodesV3Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"id":1,"name":"English"},{"id":2,"name":"Original"}]`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv, "k").LanguageProfiles(context.Background())
	if err != nil {
		t.Fatalf("LanguageProfiles: %v", err)
	}
	if len(got) != 2 || got[0].Name != "English" {
		t.Fatalf("got %+v", got)
	}
}

func TestLanguageProfiles_5xxBubblesUp(t *testing.T) {
	// 500 from Sonarr is a real outage, NOT v4-removed-endpoint. Don't
	// silently swallow it — the admin needs to know the upstream is
	// broken.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newTestClient(srv, "k").LanguageProfiles(context.Background())
	if err == nil {
		t.Fatal("500 must surface as an error, not be silently emptied")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("500 should not be classified as ErrNotFound")
	}
}

func TestAddSeries_PostsRequest(t *testing.T) {
	var got AddSeriesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/series" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"id":7,"title":"Severance"}`)
	}))
	defer srv.Close()

	req := AddSeriesRequest{
		Title: "Severance", TVDBID: 371980, Year: 2022, TitleSlug: "severance",
		Seasons:          []SeriesSeason{{SeasonNumber: 1, Monitored: true}},
		QualityProfileID: 1, LanguageProfileID: 1, RootFolderPath: "/tv",
		Monitored: true, SeasonFolder: true,
		AddOptions: AddSeriesOptions{Monitor: "all", SearchForMissingEpisodes: true},
	}
	resp, err := newTestClient(srv, "k").AddSeries(context.Background(), req)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if resp.ID != 7 || resp.Title != "Severance" {
		t.Errorf("response = %+v", resp)
	}
	if got.TVDBID != 371980 || len(got.Seasons) != 1 || got.AddOptions.Monitor != "all" {
		t.Errorf("body posted to upstream = %+v", got)
	}
}

func TestSonarrCalendar_QueryAndDecode(t *testing.T) {
	// includeSeries=true is what makes the series block (title, path,
	// certification, network, images) come back on every row.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/calendar" {
			t.Errorf("path = %q, want /api/v3/calendar", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("start") != "2026-09-26T00:00:00Z" || q.Get("end") != "2026-10-27T00:00:00Z" {
			t.Errorf("window = %q..%q", q.Get("start"), q.Get("end"))
		}
		if q.Get("unmonitored") != "false" || q.Get("includeSeries") != "true" {
			t.Errorf("unmonitored=%q includeSeries=%q", q.Get("unmonitored"), q.Get("includeSeries"))
		}
		_, _ = io.WriteString(w, `[{
			"id": 501, "seriesId": 7, "seasonNumber": 2, "episodeNumber": 5,
			"title": "The Long Night", "airDateUtc": "2026-09-28T01:00:00Z",
			"hasFile": true, "monitored": true, "overview": "Winter.",
			"series": {
				"title": "The Show", "year": 2024, "tmdbId": 1399, "tvdbId": 121361,
				"certification": "TV-MA", "network": "HBO",
				"images": [{"coverType": "poster", "remoteUrl": "https://artworks.thetvdb.com/p.jpg"}],
				"path": "/tv/The Show"
			}
		},{
			"id": 502, "seriesId": 7, "seasonNumber": 2, "episodeNumber": 6,
			"title": "", "airDateUtc": "2026-10-05T01:00:00.0000000",
			"hasFile": false, "monitored": true
		}]`)
	}))
	defer srv.Close()

	start := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	got, err := newTestClient(srv, "k").SonarrCalendar(context.Background(), start, end)
	if err != nil {
		t.Fatalf("SonarrCalendar: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d episodes, want 2", len(got))
	}
	e := got[0]
	if e.ID != 501 || e.SeasonNumber != 2 || e.EpisodeNumber != 5 || !e.HasFile || e.Title != "The Long Night" {
		t.Errorf("episode = %+v", e)
	}
	if want := time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC); !e.AirDateUTC.Equal(want) {
		t.Errorf("airDateUtc = %v, want %v", e.AirDateUTC, want)
	}
	if e.Series == nil {
		t.Fatal("series block not decoded")
	}
	if e.Series.Title != "The Show" || e.Series.TMDBID != 1399 || e.Series.TVDBID != 121361 ||
		e.Series.Certification != "TV-MA" || e.Series.Network != "HBO" || e.Series.Path != "/tv/The Show" {
		t.Errorf("series = %+v", e.Series)
	}
	// A zone-less .NET timestamp is read as UTC; a row without a series
	// block decodes to nil rather than an empty series.
	if want := time.Date(2026, 10, 5, 1, 0, 0, 0, time.UTC); !got[1].AirDateUTC.Equal(want) {
		t.Errorf("zone-less airDateUtc = %v, want %v", got[1].AirDateUTC, want)
	}
	if got[1].Series != nil {
		t.Errorf("series = %+v, want nil when absent", got[1].Series)
	}
}
