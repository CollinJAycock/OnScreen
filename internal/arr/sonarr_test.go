package arr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
		Seasons:           []SeriesSeason{{SeasonNumber: 1, Monitored: true}},
		QualityProfileID:  1, LanguageProfileID: 1, RootFolderPath: "/tv",
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
