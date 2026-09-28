package arr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Re-grab: ask the Radarr / Sonarr instance that manages a title to search for
// it again, optionally first marking the release it last grabbed as failed
// (which blocklists that release so the new search can't pick it again). Backs
// the admin "Re-grab" action on the Library health page, used when a user
// reports a broken file or the integrity probe marks one damaged.
//
// Search is the *arr apps' own decision: with a file still on disk they only
// grab a release that is an upgrade over it. The result reports HasFile so the
// admin UI can say so instead of implying a same-quality replacement is coming.

// ErrNotManaged means this instance doesn't manage the title (or the specific
// season / episode) being re-grabbed. Callers try the next instance and, when
// none manages it, report the item as not managed by any *arr app.
var ErrNotManaged = errors.New("arr: title not managed by this instance")

// ErrRegrabAction wraps a failure that happened after this instance was found
// to manage the title (reading its history, marking the grab failed, queueing
// the search). Callers stop there rather than trying another instance, which
// could otherwise send a second search for the same title.
var ErrRegrabAction = errors.New("arr: re-grab failed")

func regrabActionErr(err error) error {
	return fmt.Errorf("%w: %w", ErrRegrabAction, err)
}

// Search command names accepted by POST /api/v3/command.
const (
	CommandMoviesSearch  = "MoviesSearch"
	CommandEpisodeSearch = "EpisodeSearch"
	CommandSeasonSearch  = "SeasonSearch"
	CommandSeriesSearch  = "SeriesSearch"
)

// RegrabScope says what a re-grab searches for.
type RegrabScope string

// Re-grab scopes. Movie targets Radarr; the rest target Sonarr.
const (
	RegrabMovie   RegrabScope = "movie"
	RegrabEpisode RegrabScope = "episode"
	RegrabSeason  RegrabScope = "season"
	RegrabSeries  RegrabScope = "series"
)

// RegrabTarget identifies the title to re-grab by its provider ids.
//
//   - movie:   TMDBID.
//   - series:  the show's TVDBID (preferred) or TMDBID (Sonarr v4+ lookup).
//   - season:  as series, plus SeasonNumber.
//   - episode: as season, plus EpisodeNumber.
type RegrabTarget struct {
	Scope         RegrabScope
	TMDBID        int
	TVDBID        int
	SeasonNumber  int
	EpisodeNumber int
}

// RegrabResult describes what the instance was asked to do.
type RegrabResult struct {
	// Command is the search command sent (MoviesSearch, EpisodeSearch, …).
	Command string `json:"command"`
	// CommandID is the *arr command id, 0 if the response carried none.
	CommandID int `json:"command_id"`
	// ArrID / Title identify the movie or series inside the *arr app.
	ArrID int    `json:"arr_id"`
	Title string `json:"title"`
	// Blocklisted is true when the last grab was marked failed first.
	Blocklisted bool `json:"blocklisted"`
	// BlocklistedRelease is that grab's release name, when known.
	BlocklistedRelease string `json:"blocklisted_release,omitempty"`
	// HasFile reports whether the *arr app still has a file for the movie /
	// episode (nil for season and series searches). With a file present a
	// search only grabs an upgrade.
	HasFile *bool `json:"has_file,omitempty"`
}

// managedMovie is the subset of a Radarr /api/v3/movie row a re-grab needs.
type managedMovie struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	TMDBID  int    `json:"tmdbId"`
	HasFile bool   `json:"hasFile"`
}

// managedSeries is the subset of a Sonarr /api/v3/series row a re-grab needs.
type managedSeries struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	TVDBID int    `json:"tvdbId"`
}

// managedEpisode is the subset of a Sonarr /api/v3/episode row a re-grab needs.
type managedEpisode struct {
	ID            int  `json:"id"`
	SeriesID      int  `json:"seriesId"`
	SeasonNumber  int  `json:"seasonNumber"`
	EpisodeNumber int  `json:"episodeNumber"`
	HasFile       bool `json:"hasFile"`
}

// regrabHistoryRecord is the subset of a Radarr / Sonarr history row used to
// find the grab to blocklist. Radarr rows carry movieId, Sonarr rows
// episodeId; downloadId ties a grab to the import it produced.
type regrabHistoryRecord struct {
	ID          int       `json:"id"`
	EventType   string    `json:"eventType"`
	Date        Timestamp `json:"date"`
	SourceTitle string    `json:"sourceTitle"`
	DownloadID  string    `json:"downloadId"`
	EpisodeID   int       `json:"episodeId"`
	MovieID     int       `json:"movieId"`
}

// History event types (Radarr and Sonarr serialise the enum in camelCase).
const (
	historyEventGrabbed  = "grabbed"
	historyEventImported = "downloadFolderImported"
)

// commandResponse is the subset of POST /api/v3/command's reply we keep.
type commandResponse struct {
	ID int `json:"id"`
}

// Regrab resolves target on this instance and asks it to search again. With
// blocklist, the most recent grab for the target is marked failed first
// (POST /api/v3/history/failed/{id}); that blocklists the release and, when
// the app's "Redownload failed" option is on, queues its own search — the
// explicit search is still sent so a re-grab never depends on that setting
// (an identical queued command is de-duplicated by the app).
//
// Returns ErrNotManaged when this instance doesn't manage the title, an error
// wrapping ErrRegrabAction when it does but the blocklist / search failed, and
// any other error for a transport / upstream failure while looking it up.
func (c *Client) Regrab(ctx context.Context, target RegrabTarget, blocklist bool) (*RegrabResult, error) {
	switch target.Scope {
	case RegrabMovie:
		return c.regrabMovie(ctx, target, blocklist)
	case RegrabEpisode, RegrabSeason, RegrabSeries:
		return c.regrabSeries(ctx, target, blocklist)
	default:
		return nil, fmt.Errorf("arr: unknown re-grab scope %q", target.Scope)
	}
}

func (c *Client) regrabMovie(ctx context.Context, target RegrabTarget, blocklist bool) (*RegrabResult, error) {
	if target.TMDBID <= 0 {
		return nil, fmt.Errorf("%w: movie has no TMDB id", ErrNotManaged)
	}
	movie, err := c.managedMovieByTMDB(ctx, target.TMDBID)
	if err != nil {
		return nil, err
	}
	hasFile := movie.HasFile
	res := &RegrabResult{
		Command: CommandMoviesSearch,
		ArrID:   movie.ID,
		Title:   movie.Title,
		HasFile: &hasFile,
	}
	if blocklist {
		q := url.Values{"movieId": {strconv.Itoa(movie.ID)}}
		if err := c.blocklistLatestGrab(ctx, "/api/v3/history/movie", q, nil, res); err != nil {
			return nil, regrabActionErr(err)
		}
	}
	id, err := c.sendCommand(ctx, map[string]any{
		"name":     CommandMoviesSearch,
		"movieIds": []int{movie.ID},
	})
	if err != nil {
		return nil, regrabActionErr(err)
	}
	res.CommandID = id
	return res, nil
}

func (c *Client) regrabSeries(ctx context.Context, target RegrabTarget, blocklist bool) (*RegrabResult, error) {
	series, err := c.managedSeries(ctx, target)
	if err != nil {
		return nil, err
	}
	res := &RegrabResult{ArrID: series.ID, Title: series.Title}

	var (
		command    map[string]any
		historyQ   = url.Values{"seriesId": {strconv.Itoa(series.ID)}}
		episodeIDs map[int]bool
	)
	switch target.Scope {
	case RegrabEpisode:
		ep, err := c.managedEpisode(ctx, series.ID, target.SeasonNumber, target.EpisodeNumber)
		if err != nil {
			return nil, err
		}
		hasFile := ep.HasFile
		res.HasFile = &hasFile
		res.Command = CommandEpisodeSearch
		command = map[string]any{"name": CommandEpisodeSearch, "episodeIds": []int{ep.ID}}
		historyQ.Set("seasonNumber", strconv.Itoa(target.SeasonNumber))
		episodeIDs = map[int]bool{ep.ID: true}
	case RegrabSeason:
		// Confirm Sonarr knows the season before searching it; a SeasonSearch
		// for a season it doesn't have is accepted and silently does nothing.
		eps, err := c.seriesEpisodes(ctx, series.ID, target.SeasonNumber)
		if err != nil {
			return nil, err
		}
		if len(eps) == 0 {
			return nil, fmt.Errorf("%w: season %d not in series", ErrNotManaged, target.SeasonNumber)
		}
		res.Command = CommandSeasonSearch
		command = map[string]any{"name": CommandSeasonSearch, "seriesId": series.ID, "seasonNumber": target.SeasonNumber}
		historyQ.Set("seasonNumber", strconv.Itoa(target.SeasonNumber))
	default: // RegrabSeries
		res.Command = CommandSeriesSearch
		command = map[string]any{"name": CommandSeriesSearch, "seriesId": series.ID}
	}

	if blocklist {
		var keep func(regrabHistoryRecord) bool
		if episodeIDs != nil {
			keep = func(h regrabHistoryRecord) bool { return episodeIDs[h.EpisodeID] }
		}
		if err := c.blocklistLatestGrab(ctx, "/api/v3/history/series", historyQ, keep, res); err != nil {
			return nil, regrabActionErr(err)
		}
	}
	id, err := c.sendCommand(ctx, command)
	if err != nil {
		return nil, regrabActionErr(err)
	}
	res.CommandID = id
	return res, nil
}

// managedMovieByTMDB finds the movie in this Radarr's library. Radarr filters
// GET /api/v3/movie by tmdbId; the result is re-checked so a build that
// ignored the filter (and returned the whole library) can't pick a wrong row.
func (c *Client) managedMovieByTMDB(ctx context.Context, tmdbID int) (*managedMovie, error) {
	var out []managedMovie
	q := url.Values{"tmdbId": {strconv.Itoa(tmdbID)}}
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie", q, nil, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotManaged
		}
		return nil, err
	}
	for i := range out {
		if out[i].TMDBID == tmdbID && out[i].ID > 0 {
			return &out[i], nil
		}
	}
	return nil, ErrNotManaged
}

// managedSeries finds the show in this Sonarr's library, by TVDB id when the
// item has one, else by resolving the TMDB id to a TVDB id through Sonarr's
// own lookup (Sonarr v4+).
func (c *Client) managedSeries(ctx context.Context, target RegrabTarget) (*managedSeries, error) {
	tvdbID := target.TVDBID
	if tvdbID <= 0 && target.TMDBID > 0 {
		look, err := c.LookupSeriesByTMDB(ctx, target.TMDBID)
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, ErrNotManaged
		case err != nil:
			return nil, err
		}
		// The lookup falls back to its first result when nothing matches the
		// id exactly; only trust an exact match here.
		if look.TMDBID != target.TMDBID {
			return nil, ErrNotManaged
		}
		tvdbID = look.TVDBID
	}
	if tvdbID <= 0 {
		return nil, fmt.Errorf("%w: show has no TVDB or TMDB id", ErrNotManaged)
	}
	var out []managedSeries
	q := url.Values{"tvdbId": {strconv.Itoa(tvdbID)}}
	if err := c.do(ctx, http.MethodGet, "/api/v3/series", q, nil, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotManaged
		}
		return nil, err
	}
	for i := range out {
		if out[i].TVDBID == tvdbID && out[i].ID > 0 {
			return &out[i], nil
		}
	}
	return nil, ErrNotManaged
}

// seriesEpisodes lists one season's episodes (GET /api/v3/episode).
func (c *Client) seriesEpisodes(ctx context.Context, seriesID, season int) ([]managedEpisode, error) {
	var out []managedEpisode
	q := url.Values{
		"seriesId":     {strconv.Itoa(seriesID)},
		"seasonNumber": {strconv.Itoa(season)},
	}
	if err := c.do(ctx, http.MethodGet, "/api/v3/episode", q, nil, &out); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotManaged
		}
		return nil, err
	}
	// Re-check the season in case the filter was ignored.
	kept := out[:0]
	for _, ep := range out {
		if ep.SeasonNumber == season && ep.ID > 0 {
			kept = append(kept, ep)
		}
	}
	return kept, nil
}

// managedEpisode finds one episode of a managed series.
func (c *Client) managedEpisode(ctx context.Context, seriesID, season, episode int) (*managedEpisode, error) {
	eps, err := c.seriesEpisodes(ctx, seriesID, season)
	if err != nil {
		return nil, err
	}
	for i := range eps {
		if eps[i].EpisodeNumber == episode {
			return &eps[i], nil
		}
	}
	return nil, fmt.Errorf("%w: S%02dE%02d not in series", ErrNotManaged, season, episode)
}

// blocklistLatestGrab marks the grab that produced the current file failed.
// It prefers the grab whose download was most recently imported (that is the
// release on disk), falling back to the most recent grab. keep, when set,
// narrows the history to the target's rows (one episode of a season). Finding
// no grab at all — a file imported by hand, or history pruned — isn't an
// error: the search still runs, just without a blocklist entry.
func (c *Client) blocklistLatestGrab(ctx context.Context, path string, q url.Values, keep func(regrabHistoryRecord) bool, res *RegrabResult) error {
	var hist []regrabHistoryRecord
	if err := c.do(ctx, http.MethodGet, path, q, nil, &hist); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	grab := pickGrabToBlocklist(hist, keep)
	if grab == nil {
		return nil
	}
	if err := c.do(ctx, http.MethodPost, "/api/v3/history/failed/"+strconv.Itoa(grab.ID), nil, nil, nil); err != nil {
		return err
	}
	res.Blocklisted = true
	res.BlocklistedRelease = grab.SourceTitle
	return nil
}

// pickGrabToBlocklist chooses the history row to mark failed; nil when the
// history has no grab.
func pickGrabToBlocklist(hist []regrabHistoryRecord, keep func(regrabHistoryRecord) bool) *regrabHistoryRecord {
	rows := make([]regrabHistoryRecord, 0, len(hist))
	for _, h := range hist {
		if keep == nil || keep(h) {
			rows = append(rows, h)
		}
	}
	// Newest first; id breaks ties (ids are assigned in insert order).
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Date.Equal(rows[j].Date.Time) {
			return rows[i].Date.After(rows[j].Date.Time)
		}
		return rows[i].ID > rows[j].ID
	})
	for _, imp := range rows {
		if imp.EventType != historyEventImported || imp.DownloadID == "" {
			continue
		}
		for i := range rows {
			if rows[i].EventType == historyEventGrabbed && rows[i].DownloadID == imp.DownloadID {
				return &rows[i]
			}
		}
		break // only the latest import names the release on disk
	}
	for i := range rows {
		if rows[i].EventType == historyEventGrabbed {
			return &rows[i]
		}
	}
	return nil
}

// sendCommand posts a command body and returns the queued command's id.
func (c *Client) sendCommand(ctx context.Context, body map[string]any) (int, error) {
	var out commandResponse
	if err := c.do(ctx, http.MethodPost, "/api/v3/command", nil, body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}
