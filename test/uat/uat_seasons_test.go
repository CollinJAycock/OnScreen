package uat

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onscreen/onscreen/internal/api"
	v1 "github.com/onscreen/onscreen/internal/api/v1"
	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/requests"
)

// ── requests.DB: season-level TV requests (migration 00028) ──────────────────

func (s *stubRequestsDB) ListOwnedEpisodeCountsByShowTMDB(context.Context, gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error) {
	return nil, nil
}
func (s *stubRequestsDB) MarkMediaRequestSeasonAvailable(context.Context, gen.MarkMediaRequestSeasonAvailableParams) (int64, error) {
	return 0, nil
}

var _ requests.DB = (*stubRequestsDB)(nil)

// ── Discover — a show's seasons (authenticated users) ────────────────────────

type stubSeasonTMDB struct{}

func (stubSeasonTMDB) GetTVSeasons(_ context.Context, id int) (*metadata.TVSeasonList, error) {
	return &metadata.TVSeasonList{
		TMDBID: id,
		Seasons: []metadata.TVSeasonSummary{
			{Number: 1, Name: "Season 1", EpisodeCount: 8, AirDate: time.Date(2022, 2, 18, 0, 0, 0, 0, time.UTC)},
			{Number: 0, Name: "Specials", EpisodeCount: 1},
		},
		LastAiredSeason:  1,
		LastAiredEpisode: 8,
	}, nil
}

type stubSeasonDB struct{}

func (stubSeasonDB) ListOwnedEpisodeCountsByShowTMDB(context.Context, gen.ListOwnedEpisodeCountsByShowTMDBParams) ([]gen.ListOwnedEpisodeCountsByShowTMDBRow, error) {
	return []gen.ListOwnedEpisodeCountsByShowTMDBRow{{TmdbID: 95396, SeasonNumber: 1, EpisodeCount: 8}}, nil
}

func seasonsDiscover() func(h *api.Handlers) {
	return func(h *api.Handlers) {
		h.Discover = v1.NewDiscoverHandler(&stubDiscoverDB{}, &stubDiscoverTMDB{}, &stubDiscoverRequests{}, slog.Default()).
			WithSeasons(stubSeasonTMDB{}, stubSeasonDB{})
	}
}

func TestDiscoverSeasons_RequiresAuth(t *testing.T) {
	ts := newExtrasServer(t, seasonsDiscover())
	resp := ts.do("GET", "/api/v1/discover/tv/95396/seasons", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestDiscoverSeasons_UserGetsSeasons(t *testing.T) {
	ts := newExtrasServer(t, seasonsDiscover())
	resp := ts.do("GET", "/api/v1/discover/tv/95396/seasons", ts.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, readBody(resp))
	}
	var body struct {
		Data []struct {
			SeasonNumber  int     `json:"season_number"`
			OwnedEpisodes int     `json:"owned_episodes"`
			AiredEpisodes int     `json:"aired_episodes"`
			Requested     *string `json:"requested"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 2 || body.Data[0].SeasonNumber != 1 || body.Data[1].SeasonNumber != 0 {
		t.Fatalf("seasons = %+v, want season 1 then specials", body.Data)
	}
	if body.Data[0].OwnedEpisodes != 8 || body.Data[0].AiredEpisodes != 8 || body.Data[0].Requested != nil {
		t.Errorf("season 1 = %+v", body.Data[0])
	}
}

func TestDiscoverSeasons_NotWiredIs503(t *testing.T) {
	ts := newExtrasServer(t, func(h *api.Handlers) {
		h.Discover = v1.NewDiscoverHandler(&stubDiscoverDB{}, &stubDiscoverTMDB{}, &stubDiscoverRequests{}, slog.Default())
	})
	resp := ts.do("GET", "/api/v1/discover/tv/95396/seasons", ts.userToken(), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestDiscoverSeasons_BadID(t *testing.T) {
	ts := newExtrasServer(t, seasonsDiscover())
	resp := ts.do("GET", "/api/v1/discover/tv/nope/seasons", ts.userToken(), nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
