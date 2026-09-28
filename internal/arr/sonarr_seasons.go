package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Season-level requests: a TV request names the seasons it wants, and a show
// Sonarr already manages gets those seasons switched to monitored (and
// searched) instead of the add being treated as a no-op. The per-season
// statistics also decide when a season request is complete.

// SeasonStatistics is Sonarr's per-season tally — the statistics block of a
// series resource's seasons[] entry. EpisodeCount counts the season's
// monitored episodes that have aired (plus any that already have a file);
// EpisodeFileCount the episodes that have a file.
type SeasonStatistics struct {
	EpisodeFileCount  int       `json:"episodeFileCount"`
	EpisodeCount      int       `json:"episodeCount"`
	TotalEpisodeCount int       `json:"totalEpisodeCount"`
	PercentOfEpisodes FlexFloat `json:"percentOfEpisodes"`
}

// SeriesSeasonState is one season of a series in Sonarr's library.
type SeriesSeasonState struct {
	SeasonNumber int               `json:"seasonNumber"`
	Monitored    bool              `json:"monitored"`
	Statistics   *SeasonStatistics `json:"statistics"`
}

// Aired reports whether the season has an episode Sonarr counts as due: a
// monitored episode that has aired, or one that already has a file.
func (s SeriesSeasonState) Aired() bool {
	return s.Statistics != nil && s.Statistics.EpisodeCount > 0
}

// Complete reports whether every aired, monitored episode of the season has a
// file. A season with nothing aired yet is not complete.
func (s SeriesSeasonState) Complete() bool {
	return s.Aired() && s.Statistics.EpisodeFileCount >= s.Statistics.EpisodeCount
}

// Season returns the series' season n; ok is false when Sonarr lists none by
// that number (Sonarr numbers seasons the TVDB way, which can differ from
// TMDB's for some shows).
func (s *Series) Season(n int) (SeriesSeasonState, bool) {
	if s == nil {
		return SeriesSeasonState{}, false
	}
	for _, se := range s.Seasons {
		if se.SeasonNumber == n {
			return se, true
		}
	}
	return SeriesSeasonState{}, false
}

// SeriesSeasonHistory returns the history Sonarr recorded for one season of a
// series (GET /api/v3/history/series?seriesId=&seasonNumber=).
func (c *Client) SeriesSeasonHistory(ctx context.Context, seriesID, season int) ([]HistoryRecord, error) {
	return c.history(ctx, "/api/v3/history/series", url.Values{
		"seriesId":     {strconv.Itoa(seriesID)},
		"seasonNumber": {strconv.Itoa(season)},
	})
}

// MonitorSeasonsResult reports what MonitorSeasons changed on the series.
type MonitorSeasonsResult struct {
	// SeriesID is the Sonarr series id.
	SeriesID int
	// Monitored lists the seasons switched from unmonitored to monitored.
	Monitored []int
	// Missing lists requested seasons the series doesn't have in Sonarr.
	Missing []int
	// Searched lists the seasons a SeasonSearch was queued for.
	Searched []int
	// SearchErr is the first failed search command, if any. The monitoring
	// change stands either way — Sonarr's RSS sync still picks up releases.
	SearchErr error
}

// MonitorSeasons makes an existing Sonarr series monitor the given seasons
// and queues a SeasonSearch for each season it newly monitors (every
// requested season, when the series itself was unmonitored). An empty
// seasons list means every season except specials (season 0), matching
// Sonarr's own "all episodes" option.
//
// The series is read with GET /api/v3/series/{id} and written back whole with
// PUT /api/v3/series/{id}: only the seasons' monitored flags (and the
// series-level monitored flag, when it was off) change, and every other field
// is sent back exactly as Sonarr returned it — including ones this client
// doesn't model — so the update can't reset the admin's profile, path or
// tags. Seasons that aren't requested keep their monitoring. Sonarr cascades
// a season's new monitored flag to its episodes. When nothing needed to
// change, no PUT is sent and nothing is searched.
func (c *Client) MonitorSeasons(ctx context.Context, seriesID int, seasons []int) (*MonitorSeasonsResult, error) {
	if seriesID <= 0 {
		return nil, fmt.Errorf("arr: monitor seasons: invalid series id %d", seriesID)
	}
	path := "/api/v3/series/" + strconv.Itoa(seriesID)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &raw); err != nil {
		return nil, err
	}
	var series map[string]json.RawMessage
	if err := decodeObject(raw, &series); err != nil {
		return nil, fmt.Errorf("arr: decode series: %w", err)
	}
	if series == nil {
		return nil, ErrNotFound
	}
	var list []map[string]json.RawMessage
	if rs, ok := series["seasons"]; ok && len(rs) > 0 && string(rs) != "null" {
		if err := json.Unmarshal(rs, &list); err != nil {
			return nil, fmt.Errorf("arr: decode series seasons: %w", err)
		}
	}

	want := map[int]bool{}
	for _, n := range seasons {
		want[n] = true
	}
	all := len(seasons) == 0
	// Sonarr only grabs for a series that is itself monitored. A request is a
	// request to have the show, so an unmonitored series is switched on, and
	// its requested seasons are searched even if they were already monitored
	// (nothing was looking for them).
	var seriesMonitored bool
	_ = json.Unmarshal(series["monitored"], &seriesMonitored)

	res := &MonitorSeasonsResult{SeriesID: seriesID}
	have := map[int]bool{}
	var search []int
	changed := false
	for _, se := range list {
		var n int
		if json.Unmarshal(se["seasonNumber"], &n) != nil {
			continue
		}
		have[n] = true
		if !(all && n > 0) && !want[n] {
			continue
		}
		var monitored bool
		_ = json.Unmarshal(se["monitored"], &monitored)
		if !monitored {
			se["monitored"] = json.RawMessage("true")
			res.Monitored = append(res.Monitored, n)
			changed = true
		}
		if !monitored || !seriesMonitored {
			search = append(search, n)
		}
	}
	for n := range want {
		if !have[n] {
			res.Missing = append(res.Missing, n)
		}
	}
	sort.Ints(res.Missing)
	sort.Ints(res.Monitored)
	sort.Ints(search)

	if len(search) > 0 && !seriesMonitored {
		series["monitored"] = json.RawMessage("true")
		changed = true
	}
	if !changed {
		return res, nil
	}
	seasonsJSON, err := json.Marshal(list)
	if err != nil {
		return nil, fmt.Errorf("arr: encode series seasons: %w", err)
	}
	series["seasons"] = seasonsJSON
	if err := c.do(ctx, http.MethodPut, path, nil, series, nil); err != nil {
		return nil, err
	}

	for _, n := range search {
		if _, err := c.sendCommand(ctx, map[string]any{
			"name":         CommandSeasonSearch,
			"seriesId":     seriesID,
			"seasonNumber": n,
		}); err != nil {
			if res.SearchErr == nil {
				res.SearchErr = err
			}
			continue
		}
		res.Searched = append(res.Searched, n)
	}
	return res, nil
}
