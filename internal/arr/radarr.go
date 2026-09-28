package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MovieLookup is the subset of Radarr's /api/v3/movie/lookup result we need
// to confirm a TMDB title resolves and to populate the AddMovie request body
// (Radarr requires the lookup payload echoed back unchanged for fields like
// `images` and `year` rather than just the tmdbId).
type MovieLookup struct {
	Title         string       `json:"title"`
	OriginalTitle string       `json:"originalTitle,omitempty"`
	TMDBID        int          `json:"tmdbId"`
	Year          int          `json:"year"`
	TitleSlug     string       `json:"titleSlug"`
	Overview      string       `json:"overview,omitempty"`
	Images        []MovieImage `json:"images,omitempty"`
}

// MovieImage is Radarr's image descriptor (poster, fanart, etc.).
type MovieImage struct {
	CoverType string `json:"coverType"`
	URL       string `json:"url"`
	RemoteURL string `json:"remoteUrl,omitempty"`
}

// LookupMovieByTMDB calls /api/v3/movie/lookup with `tmdb:<id>`. Returns
// ErrNotFound if Radarr can't find a match.
func (c *Client) LookupMovieByTMDB(ctx context.Context, tmdbID int) (*MovieLookup, error) {
	q := url.Values{"term": {fmt.Sprintf("tmdb:%d", tmdbID)}}
	var results []MovieLookup
	if err := c.do(ctx, http.MethodGet, "/api/v3/movie/lookup", q, nil, &results); err != nil {
		return nil, err
	}
	for i := range results {
		if results[i].TMDBID == tmdbID {
			return &results[i], nil
		}
	}
	if len(results) > 0 {
		return &results[0], nil
	}
	return nil, ErrNotFound
}

// AddMovieOptions controls Radarr's onAdd behavior — search-on-add is the
// flag almost every caller wants since the whole point of the request is
// "go fetch this now."
type AddMovieOptions struct {
	SearchForMovie          bool `json:"searchForMovie"`
	IgnoreEpisodesWithFiles bool `json:"ignoreEpisodesWithFiles,omitempty"`
}

// AddMovieRequest is the body accepted by POST /api/v3/movie. Radarr requires
// echoing back the lookup payload (title, year, tmdbId, titleSlug, images)
// alongside the user-chosen profile / folder / tags / minimum availability.
type AddMovieRequest struct {
	Title               string          `json:"title"`
	OriginalTitle       string          `json:"originalTitle,omitempty"`
	TMDBID              int             `json:"tmdbId"`
	Year                int             `json:"year"`
	TitleSlug           string          `json:"titleSlug"`
	Images              []MovieImage    `json:"images,omitempty"`
	QualityProfileID    int32           `json:"qualityProfileId"`
	RootFolderPath      string          `json:"rootFolderPath"`
	Monitored           bool            `json:"monitored"`
	MinimumAvailability string          `json:"minimumAvailability,omitempty"`
	Tags                []int32         `json:"tags,omitempty"`
	AddOptions          AddMovieOptions `json:"addOptions"`
}

// AddMovieResponse mirrors Radarr's POST response — only the ID is currently
// useful; we keep the title for log messages.
type AddMovieResponse struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

// AddMovie posts to /api/v3/movie. ErrConflict signals Radarr already manages
// this title (Radarr answers 400 MovieExistsValidator); the caller should
// switch to the existing movie (MovieByTMDB + MonitorMovie).
func (c *Client) AddMovie(ctx context.Context, req AddMovieRequest) (*AddMovieResponse, error) {
	var out AddMovieResponse
	if err := c.do(ctx, http.MethodPost, "/api/v3/movie", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MonitorMovieResult reports what MonitorMovie did to an existing movie.
type MonitorMovieResult struct {
	// MovieID / Title identify the movie in Radarr.
	MovieID int
	Title   string
	// Monitored is true when the movie was switched from unmonitored on.
	Monitored bool
	// HasFile reports whether Radarr already has a file for the movie.
	HasFile bool
	// Searched is true when a MoviesSearch was queued.
	Searched bool
	// SearchErr is the failed search command, if any. The monitoring change
	// stands either way — Radarr's RSS sync still picks up releases.
	SearchErr error
}

// MonitorMovie is the add of a movie Radarr already manages: the movie is made
// monitored and, unless Radarr already has its file, a MoviesSearch is queued
// for it — what the add's searchForMovie would have done. A movie that is
// monitored and still missing is searched too: that is a re-request after a
// failed download, and nothing else would look for it now. With a file on
// disk a search could only grab an upgrade, which the request didn't ask for.
//
// The movie is read with GET /api/v3/movie/{id} and, only when it was
// unmonitored, written back whole with PUT /api/v3/movie/{id}: the monitored
// flag is the one change, and every other field is sent back exactly as Radarr
// returned it — including ones this client doesn't model — so the update
// can't reset the admin's profile, path or tags.
func (c *Client) MonitorMovie(ctx context.Context, movieID int) (*MonitorMovieResult, error) {
	if movieID <= 0 {
		return nil, fmt.Errorf("arr: monitor movie: invalid movie id %d", movieID)
	}
	path := "/api/v3/movie/" + strconv.Itoa(movieID)
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &raw); err != nil {
		return nil, err
	}
	var movie map[string]json.RawMessage
	if err := decodeObject(raw, &movie); err != nil {
		return nil, fmt.Errorf("arr: decode movie: %w", err)
	}
	var id int
	if json.Unmarshal(movie["id"], &id) != nil || id != movieID {
		return nil, ErrNotFound
	}
	res := &MonitorMovieResult{MovieID: movieID}
	var monitored bool
	_ = json.Unmarshal(movie["title"], &res.Title)
	_ = json.Unmarshal(movie["hasFile"], &res.HasFile)
	_ = json.Unmarshal(movie["monitored"], &monitored)

	if !monitored {
		movie["monitored"] = json.RawMessage("true")
		if err := c.do(ctx, http.MethodPut, path, nil, movie, nil); err != nil {
			return nil, err
		}
		res.Monitored = true
	}
	if res.HasFile {
		return res, nil
	}
	if _, err := c.sendCommand(ctx, map[string]any{
		"name":     CommandMoviesSearch,
		"movieIds": []int{movieID},
	}); err != nil {
		res.SearchErr = err
		return res, nil
	}
	res.Searched = true
	return res, nil
}

// CalendarMovie is the subset of a Radarr /api/v3/calendar row the Upcoming
// view needs. A movie carries up to three release dates; Radarr returns it
// when ANY of them falls inside the requested window, so callers must check
// each date against the window themselves. Path is Radarr's own view of the
// movie folder — a remote path until mapped through arr_path_mappings.
type CalendarMovie struct {
	ID              int          `json:"id"`
	Title           string       `json:"title"`
	Year            int          `json:"year"`
	TMDBID          int          `json:"tmdbId"`
	Overview        string       `json:"overview"`
	InCinemas       Timestamp    `json:"inCinemas"`
	DigitalRelease  Timestamp    `json:"digitalRelease"`
	PhysicalRelease Timestamp    `json:"physicalRelease"`
	HasFile         bool         `json:"hasFile"`
	Monitored       bool         `json:"monitored"`
	Certification   string       `json:"certification"`
	Images          []MovieImage `json:"images"`
	Path            string       `json:"path"`
}

// RadarrCalendar calls /api/v3/calendar for monitored movies with a cinema,
// digital or physical release date between start and end.
func (c *Client) RadarrCalendar(ctx context.Context, start, end time.Time) ([]CalendarMovie, error) {
	var out []CalendarMovie
	if err := c.do(ctx, http.MethodGet, "/api/v3/calendar", calendarQuery(start, end), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
