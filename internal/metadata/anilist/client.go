// Package anilist queries the AniList GraphQL API for anime metadata.
//
// AniList is the v2.2 primary anime metadata source. The API is open
// (no API key required for read queries), GraphQL-only, and returns
// rich media metadata including the cross-reference MyAnimeList ID
// (idMal) on every Media row, so we can populate both anilist_id and
// mal_id from a single round-trip.
//
// Coverage: TV-format anime is the common case. AniList also has
// MOVIE / TV_SHORT / SPECIAL / OVA / ONA / MUSIC formats; this first
// cut handles TV + TV_SHORT (returned as TVShowResult) and MOVIE
// (returned as MovieResult). OVA / ONA / SPECIAL / MUSIC handling
// arrives with the OVA/ONA/special-episode track in the v2.2 roadmap.
//
// Rate limiting: 90 req/min for anonymous (per AniList's published
// limit). The client doesn't bake in a token bucket — the scanner is
// the only caller today, and library scans are batched coarsely
// enough that 90/min covers them. If a 429 is returned, the error is
// surfaced verbatim; the scheduled-tasks scanner backs off via the
// existing per-task retry policy. Add a token bucket here when a
// second high-volume caller appears.
//
// HTML-stripping: AniList descriptions ship HTML (`<br>` line breaks,
// `<i>` italics, occasional `<a href>` links). We strip to plain text
// in mediaToCommonFields so the existing summary surfaces don't have
// to render markup. UI-rich rendering is a future enhancement.
package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/safehttp"
)

const (
	defaultEndpoint = "https://graphql.anilist.co"
	defaultUA       = "OnScreen/2.2 (https://github.com/CollinJAycock/OnScreen)"
)

// Client wraps a GraphQL HTTP client pointed at graphql.anilist.co.
type Client struct {
	http      *http.Client
	endpoint  string
	userAgent string
}

// New returns a Client with a 10 s timeout and the default User-Agent.
// The endpoint is fixed in production; tests use NewWithEndpoint to
// point at httptest.Server.
func New() *Client {
	return &Client{
		// safehttp blocks post-resolution loopback / RFC1918 / link-local
		// dials. graphql.anilist.co is public, but this closes the
		// DNS-rebinding TOCTOU window for every public-API client uniformly.
		http:      safehttp.NewClient(safehttp.DialPolicy{}, 10*time.Second),
		endpoint:  defaultEndpoint,
		userAgent: defaultUA,
	}
}

// NewWithEndpoint is a test seam that lets us point the client at
// httptest.Server. Uses a stock http.Client (no safehttp wrapper) so
// loopback addresses from httptest.NewServer aren't blocked at dial.
func NewWithEndpoint(endpoint string) *Client {
	return &Client{
		http:      &http.Client{Timeout: 10 * time.Second},
		endpoint:  endpoint,
		userAgent: defaultUA,
	}
}

// SearchAnime returns the top TV-format anime match for the given
// title. year filters by seasonYear when > 0; pass 0 to match any
// year. Returns a non-nil error if no TV-format match exists — caller
// is expected to fall through to the next agent in the chain (TMDB /
// TVDB) or surface the error.
func (c *Client) SearchAnime(ctx context.Context, title string, year int) (*metadata.TVShowResult, error) {
	const q = `
		query ($search: String, $year: Int) {
			Media(search: $search, type: ANIME, format_in: [TV, TV_SHORT], seasonYear: $year, sort: SEARCH_MATCH) {
				id idMal
				title { romaji english native }
				format
				episodes
				description(asHtml: false)
				averageScore
				seasonYear
				genres
				countryOfOrigin
				isAdult
				coverImage { extraLarge color }
				bannerImage
			}
		}
	`
	vars := map[string]interface{}{"search": title}
	if year > 0 {
		vars["year"] = year
	}

	var resp struct {
		Data struct {
			Media *anilistMedia `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, vars, &resp); err != nil {
		return nil, fmt.Errorf("anilist search anime %q: %w", title, err)
	}
	if resp.Data.Media == nil {
		return nil, fmt.Errorf("anilist: no anime match for %q", title)
	}
	return mediaToTVShowResult(*resp.Data.Media), nil
}

// SearchAnimeMovie returns the top MOVIE-format anime match. Same
// signature shape as SearchAnime but constrains format to MOVIE.
func (c *Client) SearchAnimeMovie(ctx context.Context, title string, year int) (*metadata.MovieResult, error) {
	const q = `
		query ($search: String, $year: Int) {
			Media(search: $search, type: ANIME, format: MOVIE, seasonYear: $year, sort: SEARCH_MATCH) {
				id idMal
				title { romaji english native }
				format
				duration
				description(asHtml: false)
				averageScore
				seasonYear
				startDate { year month day }
				genres
				countryOfOrigin
				isAdult
				coverImage { extraLarge color }
				bannerImage
			}
		}
	`
	vars := map[string]interface{}{"search": title}
	if year > 0 {
		vars["year"] = year
	}

	var resp struct {
		Data struct {
			Media *anilistMedia `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, vars, &resp); err != nil {
		return nil, fmt.Errorf("anilist search anime movie %q: %w", title, err)
	}
	if resp.Data.Media == nil {
		return nil, fmt.Errorf("anilist: no anime movie match for %q", title)
	}
	return mediaToMovieResult(*resp.Data.Media), nil
}

// SearchAnimeCandidates returns up to 10 TV-format anime matches for
// the given query string, intended for manual-match disambiguation in
// the admin UI. Sorted by AniList's relevance ranking
// (SEARCH_MATCH).
func (c *Client) SearchAnimeCandidates(ctx context.Context, query string) ([]metadata.TVShowResult, error) {
	const q = `
		query ($search: String) {
			Page(perPage: 10) {
				media(search: $search, type: ANIME, format_in: [TV, TV_SHORT], sort: SEARCH_MATCH) {
					id idMal
					title { romaji english native }
					format
					episodes
					description(asHtml: false)
					averageScore
					seasonYear
					genres
					countryOfOrigin
					isAdult
					coverImage { extraLarge }
					bannerImage
				}
			}
		}
	`
	var resp struct {
		Data struct {
			Page struct {
				Media []anilistMedia `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, map[string]interface{}{"search": query}, &resp); err != nil {
		return nil, fmt.Errorf("anilist candidates %q: %w", query, err)
	}
	out := make([]metadata.TVShowResult, 0, len(resp.Data.Page.Media))
	for _, m := range resp.Data.Page.Media {
		out = append(out, *mediaToTVShowResult(m))
	}
	return out, nil
}

// GetAnimeByID refreshes a TV-format anime by AniList Media ID. Use
// after the initial scan when re-enriching from a known anilist_id.
func (c *Client) GetAnimeByID(ctx context.Context, anilistID int) (*metadata.TVShowResult, error) {
	const q = `
		query ($id: Int) {
			Media(id: $id, type: ANIME) {
				id idMal
				title { romaji english native }
				format
				episodes
				description(asHtml: false)
				averageScore
				seasonYear
				genres
				countryOfOrigin
				isAdult
				coverImage { extraLarge color }
				bannerImage
			}
		}
	`
	var resp struct {
		Data struct {
			Media *anilistMedia `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, map[string]interface{}{"id": anilistID}, &resp); err != nil {
		return nil, fmt.Errorf("anilist get anime %d: %w", anilistID, err)
	}
	if resp.Data.Media == nil {
		return nil, fmt.Errorf("anilist: no anime with id %d", anilistID)
	}
	return mediaToTVShowResult(*resp.Data.Media), nil
}

// SearchManga returns the top manga match for the given title.
// AniList's GraphQL endpoint serves both anime and manga — the only
// query difference is `type: MANGA` plus the staff/tag fields that
// matter for manga (mangaka, demographic, magazine).
//
// Returns a non-nil error if no manga match exists; caller falls
// through to the next agent in the chain (none today — AniList is
// the only manga source we ship).
func (c *Client) SearchManga(ctx context.Context, title string, year int) (*metadata.MangaResult, error) {
	const q = `
		query ($search: String, $year: Int) {
			Media(search: $search, type: MANGA, startDate_like: $year, sort: SEARCH_MATCH) {
				id idMal
				title { romaji english native }
				format status
				volumes chapters
				description(asHtml: false)
				averageScore
				startDate { year }
				genres tags { name }
				countryOfOrigin
				isAdult
				coverImage { extraLarge }
				bannerImage
				staff(perPage: 5) { edges { role node { name { full } } } }
			}
		}
	`
	vars := map[string]interface{}{"search": title}
	if year > 0 {
		// AniList's startDate_like takes a string pattern, e.g. "2008%"
		// to match all start dates in 2008. Year-only filter.
		vars["year"] = fmt.Sprintf("%d%%", year)
	}

	var resp struct {
		Data struct {
			Media *anilistMangaMedia `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, vars, &resp); err != nil {
		return nil, fmt.Errorf("anilist search manga %q: %w", title, err)
	}
	if resp.Data.Media == nil {
		return nil, fmt.Errorf("anilist: no manga match for %q", title)
	}
	return mediaToMangaResult(*resp.Data.Media), nil
}

// GetMangaByID refreshes a manga by AniList Media ID. Use after the
// initial scan when re-enriching from a known anilist_id.
func (c *Client) GetMangaByID(ctx context.Context, anilistID int) (*metadata.MangaResult, error) {
	const q = `
		query ($id: Int) {
			Media(id: $id, type: MANGA) {
				id idMal
				title { romaji english native }
				format status
				volumes chapters
				description(asHtml: false)
				averageScore
				startDate { year }
				genres tags { name }
				countryOfOrigin
				isAdult
				coverImage { extraLarge }
				bannerImage
				staff(perPage: 5) { edges { role node { name { full } } } }
			}
		}
	`
	var resp struct {
		Data struct {
			Media *anilistMangaMedia `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, map[string]interface{}{"id": anilistID}, &resp); err != nil {
		return nil, fmt.Errorf("anilist get manga %d: %w", anilistID, err)
	}
	if resp.Data.Media == nil {
		return nil, fmt.Errorf("anilist: no manga with id %d", anilistID)
	}
	return mediaToMangaResult(*resp.Data.Media), nil
}

// SearchMangaCandidates returns up to 10 manga matches for a query
// string. Drives the manual-match disambiguation UI.
func (c *Client) SearchMangaCandidates(ctx context.Context, query string) ([]metadata.MangaResult, error) {
	const q = `
		query ($search: String) {
			Page(perPage: 10) {
				media(search: $search, type: MANGA, sort: SEARCH_MATCH) {
					id idMal
					title { romaji english native }
					format status
					volumes chapters
					description(asHtml: false)
					averageScore
					startDate { year }
					genres tags { name }
					countryOfOrigin
					isAdult
					coverImage { extraLarge }
					staff(perPage: 5) { edges { role node { name { full } } } }
				}
			}
		}
	`
	var resp struct {
		Data struct {
			Page struct {
				Media []anilistMangaMedia `json:"media"`
			} `json:"Page"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, map[string]interface{}{"search": query}, &resp); err != nil {
		return nil, fmt.Errorf("anilist manga candidates %q: %w", query, err)
	}
	out := make([]metadata.MangaResult, 0, len(resp.Data.Page.Media))
	for _, m := range resp.Data.Page.Media {
		out = append(out, *mediaToMangaResult(m))
	}
	return out, nil
}

// anilistMangaMedia mirrors the GraphQL response for manga queries.
// Distinct from anilistMedia (anime) because manga have volumes /
// chapters / staff fields that anime queries don't request, plus
// status uses a different vocabulary (RELEASING vs FINISHED instead
// of anime's airing terms).
type anilistMangaMedia struct {
	ID              int               `json:"id"`
	IDMal           int               `json:"idMal"`
	Title           anilistTitleNode  `json:"title"`
	Format          string            `json:"format"`
	Status          string            `json:"status"`
	Volumes         int               `json:"volumes"`
	Chapters        int               `json:"chapters"`
	Description     string            `json:"description"`
	AverageScore    int               `json:"averageScore"`
	StartDate       struct {
		Year int `json:"year"`
	} `json:"startDate"`
	Genres          []string          `json:"genres"`
	Tags            []struct {
		Name string `json:"name"`
	} `json:"tags"`
	CountryOfOrigin string            `json:"countryOfOrigin"`
	IsAdult         bool              `json:"isAdult"`
	CoverImage      struct {
		ExtraLarge string `json:"extraLarge"`
	} `json:"coverImage"`
	BannerImage string `json:"bannerImage"`
	Staff       struct {
		Edges []struct {
			Role string `json:"role"`
			Node struct {
				Name struct {
					Full string `json:"full"`
				} `json:"name"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"staff"`
}

// mediaToMangaResult flattens the GraphQL response into the
// metadata-package result type, handling AniList's quirks:
//
//  - title prefers English → romaji → native (same chain as anime)
//  - volumes / chapters return -1 for ongoing series (status RELEASING)
//    so the UI can render "ongoing" instead of fake-final counts
//  - staff is filtered for "Story" + "Art" / "Story & Art" roles
//    only, since AniList includes editors / translators / etc.
//  - readingDirection derives from countryOfOrigin: JP → rtl, KR / CN → ttb
//    (manhwa / manhua are vertical-strip webtoons by convention),
//    everything else → ltr
//  - isAdult flips ContentRating to TV-MA (same as anime — see
//    contentrating.Rank for the granular Japanese / MAL codes that
//    a manual edit can layer on top)
func mediaToMangaResult(m anilistMangaMedia) *metadata.MangaResult {
	r := &metadata.MangaResult{
		AniListID:           m.ID,
		MALID:               m.IDMal,
		Title:               m.Title.bestTitle(),
		OriginalTitle:       m.Title.Native,
		StartYear:           m.StartDate.Year,
		Summary:             stripHTML(m.Description),
		Genres:              m.Genres,
		SerializationStatus: m.Status,
		PosterURL:           m.CoverImage.ExtraLarge,
		BannerURL:           m.BannerImage,
	}
	if m.AverageScore > 0 {
		r.Rating = float64(m.AverageScore) / 10.0
	}
	if m.IsAdult {
		r.ContentRating = "TV-MA"
	}
	// Volumes / Chapters: 0 from AniList means "unknown / ongoing"
	// for in-flight series. Surface as -1 so callers can disambiguate
	// "0 volumes published yet" from "AniList didn't say".
	if m.Status == "RELEASING" {
		r.Volumes = -1
		r.Chapters = -1
	} else {
		r.Volumes = m.Volumes
		r.Chapters = m.Chapters
	}
	// Reading direction by origin country.
	switch m.CountryOfOrigin {
	case "KR", "CN":
		r.ReadingDirection = "ttb"
	case "JP":
		r.ReadingDirection = "rtl"
	default:
		r.ReadingDirection = "ltr"
	}
	// Tags carry demographic / magazine / sub-genre classifications.
	for _, t := range m.Tags {
		r.Tags = append(r.Tags, t.Name)
	}
	// Staff: filter to Story / Art roles. Story = author, Art = artist,
	// "Story & Art" = both. Some titles split (Death Note: Tsugumi Ohba
	// = Story, Takeshi Obata = Art); shounen mainstays are usually
	// solo.
	for _, e := range m.Staff.Edges {
		role := strings.ToLower(e.Role)
		switch {
		case strings.Contains(role, "story & art"), strings.Contains(role, "story and art"):
			if r.Author == "" {
				r.Author = e.Node.Name.Full
			}
			if r.Artist == "" {
				r.Artist = e.Node.Name.Full
			}
		case strings.Contains(role, "story") || role == "original creator":
			if r.Author == "" {
				r.Author = e.Node.Name.Full
			}
		case strings.Contains(role, "art"):
			if r.Artist == "" {
				r.Artist = e.Node.Name.Full
			}
		}
	}
	return r
}

// AniListRelation describes one Media node in the prequel/sequel
// relation chain of an anime franchise. Anime franchises on AniList
// are split: each cour / season is its own Media row, joined by
// PREQUEL / SEQUEL edges. Walking the chain from any matched row
// reveals the full franchise so the scanner can per-season-link.
type AniListRelation struct {
	AniListID int
	MalID     int
	Format    string // "TV", "TV_SHORT", "MOVIE", "OVA", "ONA", "SPECIAL"
	StartYear int    // 0 if missing
	Title     string // best-available title (English → romaji → native)
}

// GetAnimeFranchise returns the matched Media plus every PREQUEL /
// SEQUEL Media in its chain, sorted by start-year ascending. Used by
// the scanner to map our seasons (Season 1, Season 2, …) onto the
// distinct AniList Media rows that represent each cour, so each
// season can carry its own anilist_id and per-episode metadata
// resolves to the right cour.
//
// Walks the chain starting from anilistID. Includes the input row
// itself in the result. Caps the walk at maxFranchiseDepth to bound
// pathological cycles (AniList's data quality is generally good but
// occasionally has weird relation loops).
func (c *Client) GetAnimeFranchise(ctx context.Context, anilistID int) ([]AniListRelation, error) {
	const q = `
		query ($id: Int) {
			Media(id: $id, type: ANIME) {
				id idMal format
				title { romaji english native }
				startDate { year }
				relations { edges {
					relationType(version: 2)
					node {
						id idMal format
						title { romaji english native }
						startDate { year }
					}
				} }
			}
		}
	`
	visited := map[int]bool{}
	var out []AniListRelation
	queue := []int{anilistID}
	for len(queue) > 0 && len(visited) < maxFranchiseDepth {
		next := queue[0]
		queue = queue[1:]
		if visited[next] {
			continue
		}
		visited[next] = true

		var resp struct {
			Data struct {
				Media *struct {
					ID        int    `json:"id"`
					IDMal     int    `json:"idMal"`
					Format    string `json:"format"`
					Title     anilistTitleNode `json:"title"`
					StartDate struct {
						Year int `json:"year"`
					} `json:"startDate"`
					Relations struct {
						Edges []struct {
							RelationType string `json:"relationType"`
							Node         struct {
								ID        int    `json:"id"`
								IDMal     int    `json:"idMal"`
								Format    string `json:"format"`
								Title     anilistTitleNode `json:"title"`
								StartDate struct {
									Year int `json:"year"`
								} `json:"startDate"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"relations"`
				} `json:"Media"`
			} `json:"data"`
		}
		if err := c.query(ctx, q, map[string]interface{}{"id": next}, &resp); err != nil {
			return nil, fmt.Errorf("anilist franchise walk %d: %w", next, err)
		}
		if resp.Data.Media == nil {
			continue
		}
		m := resp.Data.Media
		out = append(out, AniListRelation{
			AniListID: m.ID,
			MalID:     m.IDMal,
			Format:    m.Format,
			StartYear: m.StartDate.Year,
			Title:     m.Title.bestTitle(),
		})
		for _, edge := range m.Relations.Edges {
			// Only PREQUEL / SEQUEL extend the franchise chain. Other
			// types (SIDE_STORY, SPIN_OFF, ALTERNATIVE, CHARACTER, etc.)
			// describe related-but-not-same-franchise rows and would
			// pollute the season mapping.
			if edge.RelationType != "PREQUEL" && edge.RelationType != "SEQUEL" {
				continue
			}
			// Filter to TV-shaped formats. A franchise's MOVIE / OVA /
			// SPECIAL siblings aren't seasons in our model.
			if edge.Node.Format != "TV" && edge.Node.Format != "TV_SHORT" {
				continue
			}
			if !visited[edge.Node.ID] {
				queue = append(queue, edge.Node.ID)
			}
		}
	}

	// Sort by start year ascending so callers can map output[0] → S1,
	// output[1] → S2, etc. Stable sort on year keeps the matched row
	// in its natural position when a year tie occurs (rare).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartYear < out[j].StartYear
	})
	return out, nil
}

// maxFranchiseDepth caps the franchise walk. 20 is comfortably above
// the longest currently-running TV-format anime franchise on AniList
// (One Piece's TV runs are a single Media row; Naruto + Boruto split
// is 4 rows; long-form like Conan / Doraemon are also single rows).
const maxFranchiseDepth = 20

// anilistTitleNode mirrors the AniList GraphQL `title` shape so we can
// reuse the title-fallback logic for franchise relations without
// pulling in the full anilistMedia struct.
type anilistTitleNode struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

func (t anilistTitleNode) bestTitle() string {
	if t.English != "" {
		return t.English
	}
	if t.Romaji != "" {
		return t.Romaji
	}
	return t.Native
}

// GetAnimeEpisodes returns the streamingEpisodes list for an AniList
// Media row, shaped as metadata.EpisodeResult so the enricher can
// thread it through the same plumbing as TMDB/TVDB results.
//
// Used by the enricher as a final fallback when TMDB and TVDB are
// unavailable (broken / unconfigured key) so anime episodes still
// surface a title + thumbnail strip. AniList does not publish
// per-episode summaries (it's a tracker, not an episode-data store),
// so Summary, AirDate, and Rating stay zero on the returned rows —
// callers should treat this as the "best we can do" path.
//
// AniList's streamingEpisodes is community-curated and not present
// for every show — older / niche / hentai / unlicensed-in-the-West
// titles often have nothing here. Return value is nil + nil error
// in that case so the caller can cleanly fall through to "no
// episode metadata available." EpisodeNum is parsed from the title
// field ("Episode N - …" / "Episode N: …" / "Episode N"); entries
// with non-standard titles (specials, OVAs) skip the index match
// and arrive with EpisodeNum=0.
func (c *Client) GetAnimeEpisodes(ctx context.Context, anilistID int) ([]metadata.EpisodeResult, error) {
	const q = `
		query ($id: Int) {
			Media(id: $id, type: ANIME) {
				episodes
				streamingEpisodes { title thumbnail }
			}
		}
	`
	var resp struct {
		Data struct {
			Media *struct {
				Episodes          int `json:"episodes"`
				StreamingEpisodes []struct {
					Title     string `json:"title"`
					Thumbnail string `json:"thumbnail"`
				} `json:"streamingEpisodes"`
			} `json:"Media"`
		} `json:"data"`
	}
	if err := c.query(ctx, q, map[string]interface{}{"id": anilistID}, &resp); err != nil {
		return nil, fmt.Errorf("anilist get anime episodes %d: %w", anilistID, err)
	}
	if resp.Data.Media == nil || len(resp.Data.Media.StreamingEpisodes) == 0 {
		return nil, nil
	}

	parsed := make([]metadata.EpisodeResult, 0, len(resp.Data.Media.StreamingEpisodes))
	for _, ep := range resp.Data.Media.StreamingEpisodes {
		idx, title := parseStreamingEpisodeTitle(ep.Title)
		parsed = append(parsed, metadata.EpisodeResult{
			EpisodeNum: idx,
			Title:      title,
			ThumbURL:   ep.Thumbnail,
		})
	}

	episodeCount := resp.Data.Media.Episodes

	// Sanity check 1: data quality. AniList occasionally attaches the
	// WRONG cour's streamingEpisodes to a Media row — Solo Leveling's
	// S1 row (id=151807) has S2's streamingEpisodes copy-pasted in,
	// so applying the data would give Season 1 episodes the titles of
	// Season 2 content. When the entry count disagrees with the row's
	// declared episode count, refuse the data rather than serve the
	// wrong content.
	if episodeCount > 0 && len(parsed) != episodeCount {
		return nil, nil
	}

	// Sanity check 2: absolute numbering. Multi-cour anime (Solo
	// Leveling, Bleach, Demon Slayer's Mugen Train Arc) often
	// publish streamingEpisodes with absolute numbering — Season 2
	// shows up as "Episode 13 - …" through "Episode 25 - …" rather
	// than "Episode 1 - …" through "Episode 13 - …". Detect by
	// looking for a min-EpisodeNum > 1 across the parsed list and
	// rebase by subtracting the offset so position-1 maps to
	// season-relative episode 1 cleanly.
	offset := absoluteNumberingOffset(parsed)
	if offset > 0 {
		for i := range parsed {
			if parsed[i].EpisodeNum > offset {
				parsed[i].EpisodeNum -= offset
			}
		}
	}

	// AniList returns streamingEpisodes in REVERSE chronological
	// order (latest aired first). Sort ascending by EpisodeNum so
	// position-fallback in the caller maps target N → eps[N-1] in
	// the natural reading direction. Unparsed entries (EpisodeNum=0,
	// non-standard titles like "OVA Special") sort to the END of
	// the list so they don't displace the parseable mainline.
	sort.SliceStable(parsed, func(i, j int) bool {
		ai, aj := parsed[i].EpisodeNum, parsed[j].EpisodeNum
		if ai == 0 && aj != 0 {
			return false
		}
		if aj == 0 && ai != 0 {
			return true
		}
		return ai < aj
	})

	return parsed, nil
}

// absoluteNumberingOffset detects when a streamingEpisodes list uses
// absolute numbering (multi-cour anime — Episode 13 through Episode 25
// for Season 2's 13 episodes). Returns the offset to subtract from
// each EpisodeNum to land back at season-relative numbering, or 0
// when the list already starts at episode 1 (or no parseable indices).
func absoluteNumberingOffset(eps []metadata.EpisodeResult) int {
	minEp := 0
	for _, ep := range eps {
		if ep.EpisodeNum <= 0 {
			continue
		}
		if minEp == 0 || ep.EpisodeNum < minEp {
			minEp = ep.EpisodeNum
		}
	}
	if minEp <= 1 {
		return 0
	}
	return minEp - 1
}

// streamingEpisodeTitleRE matches AniList's "Episode N" / "Episode N -
// Title" / "Episode N: Title" prefix, capturing the index and the
// trailing title. Anchored to the start of the string so partial
// matches inside titles ("The Episode 50 Special") don't trigger.
var streamingEpisodeTitleRE = regexp.MustCompile(`^Episode\s+(\d+)\s*(?:[-:]\s*(.+))?$`)

// parseStreamingEpisodeTitle pulls the episode index and bare title
// out of AniList's `streamingEpisodes.title` strings. AniList uses
// several formats — "Episode 1", "Episode 1 - Awakening", "Episode
// 1: Awakening" are common; specials sometimes use "OVA 1" or just
// the bare title. When the index can't be parsed, returns 0 + the
// original title verbatim.
func parseStreamingEpisodeTitle(s string) (int, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, ""
	}
	m := streamingEpisodeTitleRE.FindStringSubmatch(s)
	if m == nil {
		return 0, s
	}
	idx := 0
	if _, err := fmt.Sscanf(m[1], "%d", &idx); err != nil {
		return 0, s
	}
	title := strings.TrimSpace(m[2])
	return idx, title
}

// query is the shared GraphQL POST helper. variables may be nil. out
// must be a pointer to a struct shaped to match the GraphQL response.
//
// AniList signals errors two ways: HTTP 4xx/5xx status (rare; usually
// only on rate-limit 429) and an `errors` field in the JSON body
// (most query-level problems — invalid types, missing fields). Both
// are surfaced as Go errors here.
func (c *Client) query(ctx context.Context, q string, variables map[string]interface{}, out interface{}) error {
	body, err := json.Marshal(map[string]interface{}{
		"query":     q,
		"variables": variables,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("anilist HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	// Surface GraphQL-level errors before attempting to decode into
	// the caller's typed output, so a not-found / rate-limited /
	// validation error doesn't get swallowed as "Media is null".
	var gerr struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &gerr); err == nil && len(gerr.Errors) > 0 {
		return fmt.Errorf("anilist graphql: %s", gerr.Errors[0].Message)
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}

// anilistMedia is the GraphQL Media type, narrowed to the fields we
// actually request. JSON tags match AniList's camelCase convention.
type anilistMedia struct {
	ID    int `json:"id"`
	IDMal int `json:"idMal"`

	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`

	Format          string   `json:"format"`
	Episodes        int      `json:"episodes"`
	Duration        int      `json:"duration"` // minutes (movies)
	Description     string   `json:"description"`
	AverageScore    int      `json:"averageScore"` // 0-100
	SeasonYear      int      `json:"seasonYear"`
	Genres          []string `json:"genres"`
	CountryOfOrigin string   `json:"countryOfOrigin"`
	IsAdult         bool     `json:"isAdult"`

	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
		Color      string `json:"color"`
	} `json:"coverImage"`
	BannerImage string `json:"bannerImage"`

	StartDate struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"startDate"`
}

// mediaToTVShowResult maps an AniList Media into our internal
// TVShowResult shape. English title is preferred when present
// (matches what most users will type / expect to see); falls back to
// romaji, then native. Original title carries the romaji form so
// scanner-side title matching against filename hints still works.
func mediaToTVShowResult(m anilistMedia) *metadata.TVShowResult {
	return &metadata.TVShowResult{
		AniListID:     m.ID,
		MALID:         m.IDMal,
		Title:         pickTitle(m),
		OriginalTitle: m.Title.Romaji,
		FirstAirYear:  m.SeasonYear,
		Summary:       stripHTML(m.Description),
		Rating:        float64(m.AverageScore) / 10.0,
		ContentRating: animeContentRating(m.IsAdult),
		Genres:        m.Genres,
		PosterURL:     m.CoverImage.ExtraLarge,
		FanartURL:     m.BannerImage,
	}
}

// mediaToMovieResult is the MOVIE-format counterpart. Duration is
// minutes server-side; multiply to ms for our schema.
func mediaToMovieResult(m anilistMedia) *metadata.MovieResult {
	releaseDate := time.Time{}
	if m.StartDate.Year > 0 && m.StartDate.Month > 0 && m.StartDate.Day > 0 {
		releaseDate = time.Date(m.StartDate.Year, time.Month(m.StartDate.Month), m.StartDate.Day, 0, 0, 0, 0, time.UTC)
	}
	return &metadata.MovieResult{
		AniListID:     m.ID,
		MALID:         m.IDMal,
		Title:         pickTitle(m),
		OriginalTitle: m.Title.Romaji,
		Year:          m.SeasonYear,
		Summary:       stripHTML(m.Description),
		Rating:        float64(m.AverageScore) / 10.0,
		ContentRating: animeContentRating(m.IsAdult),
		DurationMS:    int64(m.Duration) * 60_000,
		Genres:        m.Genres,
		ReleaseDate:   releaseDate,
		PosterURL:     m.CoverImage.ExtraLarge,
		FanartURL:     m.BannerImage,
	}
}

// pickTitle prefers English → Romaji → Native. AniList English title
// is empty for shows that don't have an official localised English
// release; romaji is always present.
func pickTitle(m anilistMedia) string {
	switch {
	case m.Title.English != "":
		return m.Title.English
	case m.Title.Romaji != "":
		return m.Title.Romaji
	default:
		return m.Title.Native
	}
}

// animeContentRating reduces AniList's `isAdult` boolean to our
// existing parental-rating string. Maps to TV-MA for the adult tier
// (matches Western TV rating users will recognise) and leaves the
// non-adult tier blank so the agent fallback chain (TMDB / TVDB) can
// fill in a more specific rating if it has one.
//
// AniList itself only exposes `isAdult` — there is no published
// R-15 / R-17+ / R-18+ tier on the AniList schema, despite the
// roadmap text suggesting otherwise. The granular Japanese / MAL
// codes (R-15, R-17+, R-18+, R+, Rx) are recognised by
// contentrating.Rank so a future MAL agent, NFO carry-through, or
// operator manual edit can attach them — but populating them from
// AniList alone is not possible.
func animeContentRating(isAdult bool) string {
	if isAdult {
		return "TV-MA"
	}
	return ""
}

// htmlTagRE strips HTML tags from the AniList description field.
// AniList's `description(asHtml: false)` already returns markdown-ish
// text, but we still see `<br>` and the occasional `<i>` slip
// through. Conservative tag-strip is enough for plain-text summary
// rendering; HTML-aware UI rendering can subscribe to the raw field
// in a follow-up.
var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

// stripHTML removes HTML tags from a description string. NBSP is
// converted to a regular space so wrapping/cleanup works downstream.
//
// HTML entities (&amp; / &lt; / &gt; / &quot; / &#39;) are NOT decoded
// here. The earlier version decoded them back to literal `& < > " '`,
// which is inert today (Svelte's `{...}` interpolation re-escapes
// unconditionally) but is a footgun: a future dev who reaches for
// `{@html item.summary}` on a metadata field would suddenly have an
// XSS vector — a malicious AniList description containing literal
// "&lt;script&gt;" would round-trip through stripHTML to `<script>`
// and inject. Leaving entities encoded means the stored summary is
// already a safe string for any future renderer.
func stripHTML(s string) string {
	out := htmlTagRE.ReplaceAllString(s, "")
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	return strings.TrimSpace(out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
