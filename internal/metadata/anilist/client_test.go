package anilist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// graphqlEcho is a tiny test server that returns a fixed body for any
// POST. The handler captures the inbound request body so the test
// can assert on the operation name / variables that were sent.
type graphqlEcho struct {
	t        *testing.T
	respBody string
	captured map[string]interface{}
}

func (g *graphqlEcho) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			g.t.Errorf("want POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			g.t.Errorf("Content-Type: got %q, want application/json", ct)
		}
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "OnScreen/") {
			g.t.Errorf("User-Agent: want OnScreen/* prefix, got %q", ua)
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &g.captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(g.respBody))
	}
}

const cowboyBebopRespBody = `{
	"data": {
		"Media": {
			"id": 1,
			"idMal": 1,
			"title": {
				"romaji": "Cowboy Bebop",
				"english": "Cowboy Bebop",
				"native": "カウボーイビバップ"
			},
			"format": "TV",
			"episodes": 26,
			"description": "In the year 2071, humanity has colonized...<br><br>Spike Spiegel, a bounty hunter...",
			"averageScore": 86,
			"seasonYear": 1998,
			"genres": ["Action", "Adventure", "Drama", "Sci-Fi"],
			"countryOfOrigin": "JP",
			"isAdult": false,
			"coverImage": {
				"extraLarge": "https://example.com/cover/1.jpg",
				"color": "#e4a15d"
			},
			"bannerImage": "https://example.com/banner/1.jpg"
		}
	}
}`

func TestSearchAnime_TopMatch(t *testing.T) {
	echo := &graphqlEcho{t: t, respBody: cowboyBebopRespBody}
	srv := httptest.NewServer(echo.handler())
	defer srv.Close()

	c := NewWithEndpoint(srv.URL)
	res, err := c.SearchAnime(context.Background(), "Cowboy Bebop", 1998)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// IDs round-trip into both anilist + mal slots.
	if res.AniListID != 1 || res.MALID != 1 {
		t.Errorf("ids: got AniListID=%d MALID=%d, want 1/1", res.AniListID, res.MALID)
	}
	// English title preferred over romaji when both present (here
	// they happen to be identical, but the field-order guarantee
	// matters for shows where they differ — e.g., "Attack on Titan"
	// vs "Shingeki no Kyojin").
	if res.Title != "Cowboy Bebop" {
		t.Errorf("title: got %q, want Cowboy Bebop", res.Title)
	}
	if res.OriginalTitle != "Cowboy Bebop" {
		t.Errorf("original title: got %q, want Cowboy Bebop", res.OriginalTitle)
	}
	if res.FirstAirYear != 1998 {
		t.Errorf("year: got %d, want 1998", res.FirstAirYear)
	}
	// AverageScore 86 / 10 = 8.6.
	if res.Rating != 8.6 {
		t.Errorf("rating: got %f, want 8.6", res.Rating)
	}
	// HTML stripped from description; <br><br> collapses to no
	// whitespace, leaving the two sentences adjacent. Acceptable
	// for plain-text rendering.
	if strings.Contains(res.Summary, "<br>") {
		t.Errorf("summary still contains HTML: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, "humanity has colonized") {
		t.Errorf("summary missing expected text, got %q", res.Summary)
	}
	if len(res.Genres) != 4 {
		t.Errorf("genres: got %d, want 4", len(res.Genres))
	}
	if res.PosterURL != "https://example.com/cover/1.jpg" {
		t.Errorf("poster: got %q", res.PosterURL)
	}
	if res.FanartURL != "https://example.com/banner/1.jpg" {
		t.Errorf("fanart: got %q", res.FanartURL)
	}
	if res.ContentRating != "" {
		t.Errorf("content rating: want empty for non-adult anime, got %q", res.ContentRating)
	}

	// Variables sent over the wire match the title + year passed to
	// SearchAnime. Verifies the GraphQL variable plumbing.
	vars, ok := echo.captured["variables"].(map[string]interface{})
	if !ok {
		t.Fatalf("captured variables not a map: %T", echo.captured["variables"])
	}
	if vars["search"] != "Cowboy Bebop" {
		t.Errorf("variables.search: got %v, want Cowboy Bebop", vars["search"])
	}
	if vars["year"].(float64) != 1998 {
		t.Errorf("variables.year: got %v, want 1998", vars["year"])
	}
}

func TestSearchAnime_TitleFallbackChain(t *testing.T) {
	// English title empty → falls back to romaji, OriginalTitle stays
	// romaji either way. Tests pickTitle()'s fallback ordering.
	body := `{
		"data": {
			"Media": {
				"id": 16498, "idMal": 16498,
				"title": {"romaji": "Shingeki no Kyojin", "english": "", "native": "進撃の巨人"},
				"format": "TV", "episodes": 25, "description": "",
				"averageScore": 84, "seasonYear": 2013,
				"genres": [], "countryOfOrigin": "JP", "isAdult": false,
				"coverImage": {"extraLarge": ""}, "bannerImage": ""
			}
		}
	}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: body}).handler())
	defer srv.Close()

	c := NewWithEndpoint(srv.URL)
	res, err := c.SearchAnime(context.Background(), "Shingeki no Kyojin", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Shingeki no Kyojin" {
		t.Errorf("title: got %q, want romaji fallback Shingeki no Kyojin", res.Title)
	}
	if res.OriginalTitle != "Shingeki no Kyojin" {
		t.Errorf("original title: got %q, want romaji", res.OriginalTitle)
	}
}

func TestSearchAnime_AdultGetsTVMA(t *testing.T) {
	body := `{
		"data": {
			"Media": {
				"id": 1, "idMal": 1,
				"title": {"romaji": "X", "english": "X", "native": "X"},
				"format": "TV", "episodes": 1, "description": "",
				"averageScore": 50, "seasonYear": 2020,
				"genres": [], "countryOfOrigin": "JP", "isAdult": true,
				"coverImage": {"extraLarge": ""}, "bannerImage": ""
			}
		}
	}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: body}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).SearchAnime(context.Background(), "X", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ContentRating != "TV-MA" {
		t.Errorf("content rating: got %q, want TV-MA for isAdult", res.ContentRating)
	}
}

func TestSearchAnime_NotFoundReturnsError(t *testing.T) {
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: `{"data":{"Media":null}}`}).handler())
	defer srv.Close()

	_, err := NewWithEndpoint(srv.URL).SearchAnime(context.Background(), "Nonexistent Anime", 0)
	if err == nil {
		t.Fatal("expected error for nil Media, got nil")
	}
	if !strings.Contains(err.Error(), "no anime match") {
		t.Errorf("error message: got %q, want 'no anime match' substring", err.Error())
	}
}

func TestQuery_GraphQLErrorSurfaced(t *testing.T) {
	// AniList signals query-level errors via the `errors` field on a
	// 200 response. The client should surface those before trying to
	// decode a missing data field as "not found".
	body := `{
		"errors": [{"message": "Variable $search expected to not be null"}],
		"data": null
	}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: body}).handler())
	defer srv.Close()

	_, err := NewWithEndpoint(srv.URL).SearchAnime(context.Background(), "", 0)
	if err == nil {
		t.Fatal("expected GraphQL error, got nil")
	}
	if !strings.Contains(err.Error(), "Variable $search") {
		t.Errorf("error: got %q, want GraphQL message surfaced", err.Error())
	}
}

func TestQuery_HTTPErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
	}))
	defer srv.Close()

	_, err := NewWithEndpoint(srv.URL).SearchAnime(context.Background(), "X", 0)
	if err == nil {
		t.Fatal("expected HTTP error, got nil")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error: got %q, want HTTP 429 surfaced", err.Error())
	}
}

func TestSearchAnimeMovie_StartDateMaps(t *testing.T) {
	body := `{
		"data": {
			"Media": {
				"id": 199, "idMal": 199,
				"title": {"romaji": "Sen to Chihiro no Kamikakushi", "english": "Spirited Away", "native": "千と千尋の神隠し"},
				"format": "MOVIE", "duration": 125, "description": "",
				"averageScore": 90, "seasonYear": 2001,
				"startDate": {"year": 2001, "month": 7, "day": 20},
				"genres": ["Adventure", "Supernatural"],
				"countryOfOrigin": "JP", "isAdult": false,
				"coverImage": {"extraLarge": ""}, "bannerImage": ""
			}
		}
	}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: body}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).SearchAnimeMovie(context.Background(), "Spirited Away", 2001)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Title != "Spirited Away" {
		t.Errorf("title: got %q", res.Title)
	}
	// 125 minutes = 7,500,000 ms.
	if res.DurationMS != 125*60_000 {
		t.Errorf("duration: got %d ms, want %d", res.DurationMS, 125*60_000)
	}
	if res.ReleaseDate.Year() != 2001 || res.ReleaseDate.Month() != 7 || res.ReleaseDate.Day() != 20 {
		t.Errorf("release date: got %v, want 2001-07-20", res.ReleaseDate)
	}
}

func TestSearchAnimeCandidates_PageShape(t *testing.T) {
	body := `{
		"data": {
			"Page": {
				"media": [
					{"id": 1, "idMal": 1, "title": {"romaji": "A", "english": "A", "native": "A"}, "format": "TV", "episodes": 12, "description": "", "averageScore": 70, "seasonYear": 2020, "genres": [], "countryOfOrigin": "JP", "isAdult": false, "coverImage": {"extraLarge": ""}, "bannerImage": ""},
					{"id": 2, "idMal": 2, "title": {"romaji": "B", "english": "B", "native": "B"}, "format": "TV", "episodes": 24, "description": "", "averageScore": 75, "seasonYear": 2021, "genres": [], "countryOfOrigin": "JP", "isAdult": false, "coverImage": {"extraLarge": ""}, "bannerImage": ""}
				]
			}
		}
	}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: body}).handler())
	defer srv.Close()

	cands, err := NewWithEndpoint(srv.URL).SearchAnimeCandidates(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("got %d candidates, want 2", len(cands))
	}
	if cands[0].AniListID != 1 || cands[1].AniListID != 2 {
		t.Errorf("candidate ids: got %d, %d, want 1, 2", cands[0].AniListID, cands[1].AniListID)
	}
}

func TestGetAnimeByID(t *testing.T) {
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: cowboyBebopRespBody}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).GetAnimeByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AniListID != 1 {
		t.Errorf("got AniListID=%d, want 1", res.AniListID)
	}
}

func TestParseStreamingEpisodeTitle(t *testing.T) {
	cases := []struct {
		in        string
		wantIdx   int
		wantTitle string
	}{
		{"Episode 1 - Awakening", 1, "Awakening"},
		{"Episode 12: Real Hunter", 12, "Real Hunter"},
		{"Episode 7", 7, ""},
		{"Episode 245 - Long form", 245, "Long form"},
		// Non-standard titles arrive with EpisodeNum=0 + verbatim title.
		{"OVA 1", 0, "OVA 1"},
		{"Special Episode", 0, "Special Episode"},
		{"", 0, ""},
		// Anchored regex must reject "Episode N" embedded mid-string.
		{"The Episode 50 Special", 0, "The Episode 50 Special"},
	}
	for _, tc := range cases {
		gotIdx, gotTitle := parseStreamingEpisodeTitle(tc.in)
		if gotIdx != tc.wantIdx || gotTitle != tc.wantTitle {
			t.Errorf("parseStreamingEpisodeTitle(%q) = (%d, %q); want (%d, %q)",
				tc.in, gotIdx, gotTitle, tc.wantIdx, tc.wantTitle)
		}
	}
}

func TestGetAnimeEpisodes(t *testing.T) {
	respBody := `{"data":{"Media":{"streamingEpisodes":[
		{"title":"Episode 1 - Awakening","thumbnail":"http://x/1.jpg"},
		{"title":"Episode 2: Hunters","thumbnail":"http://x/2.jpg"},
		{"title":"OVA Special","thumbnail":"http://x/ova.jpg"}
	]}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	eps, err := NewWithEndpoint(srv.URL).GetAnimeEpisodes(context.Background(), 158927)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 3 {
		t.Fatalf("got %d episodes, want 3", len(eps))
	}
	if eps[0].EpisodeNum != 1 || eps[0].Title != "Awakening" {
		t.Errorf("ep[0] = (%d, %q); want (1, %q)", eps[0].EpisodeNum, eps[0].Title, "Awakening")
	}
	if eps[1].EpisodeNum != 2 || eps[1].Title != "Hunters" {
		t.Errorf("ep[1] = (%d, %q); want (2, %q)", eps[1].EpisodeNum, eps[1].Title, "Hunters")
	}
	if eps[2].EpisodeNum != 0 || eps[2].Title != "OVA Special" {
		t.Errorf("ep[2] = (%d, %q); want (0, %q)", eps[2].EpisodeNum, eps[2].Title, "OVA Special")
	}
	if eps[2].ThumbURL != "http://x/ova.jpg" {
		t.Errorf("ep[2].ThumbURL = %q; want http://x/ova.jpg", eps[2].ThumbURL)
	}
}

// TestGetAnimeEpisodes_AbsoluteNumberingRebased mirrors the actual
// Solo Leveling Season 2 shape: AniList's streamingEpisodes use
// absolute episode numbering (S2E1 = "Episode 13", S2E13 =
// "Episode 25") and the array is in reverse chronological order
// (latest aired first). The client should rebase the EpisodeNums to
// season-relative (1..13) and sort ascending so position-fallback
// in the caller works correctly.
func TestGetAnimeEpisodes_AbsoluteNumberingRebased(t *testing.T) {
	respBody := `{"data":{"Media":{
		"episodes": 13,
		"streamingEpisodes": [
			{"title":"Episode 25 - On to the Next Target"},
			{"title":"Episode 24 - Are You the King of Humans"},
			{"title":"Episode 23 - It's Going to Get Even More Intense"},
			{"title":"Episode 22 - We Need a Hero"},
			{"title":"Episode 21 - It was All Worth It"},
			{"title":"Episode 20 - Looking Up Was Tiring Me Out"},
			{"title":"Episode 19 - The 10th S-Rank Hunter"},
			{"title":"Episode 18 - Don't Look Down on My Guys"},
			{"title":"Episode 17 - This Is What We're Trained to Do"},
			{"title":"Episode 16 - I Need to Stop Faking"},
			{"title":"Episode 15 - Still a Long Way to Go"},
			{"title":"Episode 14 - I Suppose You Aren't Aware"},
			{"title":"Episode 13 - You Aren't E-Rank, Are You?"}
		]
	}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	eps, err := NewWithEndpoint(srv.URL).GetAnimeEpisodes(context.Background(), 176496)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 13 {
		t.Fatalf("got %d episodes, want 13", len(eps))
	}
	// After offset rebase: eps[0] should be S2E1 (was abs Episode 13).
	if eps[0].EpisodeNum != 1 || eps[0].Title != "You Aren't E-Rank, Are You?" {
		t.Errorf("eps[0] = (%d, %q); want (1, %q)", eps[0].EpisodeNum, eps[0].Title, "You Aren't E-Rank, Are You?")
	}
	// And eps[12] should be S2E13 (was abs Episode 25).
	if eps[12].EpisodeNum != 13 || eps[12].Title != "On to the Next Target" {
		t.Errorf("eps[12] = (%d, %q); want (13, %q)", eps[12].EpisodeNum, eps[12].Title, "On to the Next Target")
	}
}

// TestGetAnimeEpisodes_RejectMismatchedCount mirrors AniList's
// data-quality bug for Solo Leveling Season 1 (id=151807): the row
// declares episodes=12, but its streamingEpisodes are S2's 13
// entries copy-pasted in. Applying the data would give Season 1
// the wrong (Season 2) titles. The client should reject the data
// when the count doesn't match the row's declared episode count.
func TestGetAnimeEpisodes_RejectMismatchedCount(t *testing.T) {
	respBody := `{"data":{"Media":{
		"episodes": 12,
		"streamingEpisodes": [
			{"title":"Episode 25 - On to the Next Target"},
			{"title":"Episode 24 - Are You the King of Humans"},
			{"title":"Episode 23 - It's Going to Get Even More Intense"},
			{"title":"Episode 22 - We Need a Hero"},
			{"title":"Episode 21 - It was All Worth It"},
			{"title":"Episode 20 - Looking Up Was Tiring Me Out"},
			{"title":"Episode 19 - The 10th S-Rank Hunter"},
			{"title":"Episode 18 - Don't Look Down on My Guys"},
			{"title":"Episode 17 - This Is What We're Trained to Do"},
			{"title":"Episode 16 - I Need to Stop Faking"},
			{"title":"Episode 15 - Still a Long Way to Go"},
			{"title":"Episode 14 - I Suppose You Aren't Aware"},
			{"title":"Episode 13 - You Aren't E-Rank, Are You?"}
		]
	}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	eps, err := NewWithEndpoint(srv.URL).GetAnimeEpisodes(context.Background(), 151807)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eps != nil {
		t.Errorf("count mismatch (12 declared vs 13 streamingEpisodes) should reject; got %d entries", len(eps))
	}
}

func TestGetAnimeEpisodes_EmptyReturnsNil(t *testing.T) {
	respBody := `{"data":{"Media":{"streamingEpisodes":[]}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	eps, err := NewWithEndpoint(srv.URL).GetAnimeEpisodes(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eps != nil {
		t.Errorf("empty list should return nil, got %v", eps)
	}
}

// ── Manga ────────────────────────────────────────────────────────────────────

const deathNoteMangaResp = `{
	"data": {
		"Media": {
			"id": 30014,
			"idMal": 21,
			"title": {
				"romaji": "Death Note",
				"english": "Death Note",
				"native": "デスノート"
			},
			"format": "MANGA",
			"status": "FINISHED",
			"volumes": 12,
			"chapters": 108,
			"description": "Light Yagami is an ace student...",
			"averageScore": 85,
			"startDate": { "year": 2003 },
			"genres": ["Mystery", "Psychological", "Supernatural", "Thriller"],
			"tags": [
				{ "name": "Shounen" },
				{ "name": "Detective" }
			],
			"countryOfOrigin": "JP",
			"isAdult": false,
			"coverImage": { "extraLarge": "https://example.com/dn.jpg" },
			"bannerImage": "https://example.com/dn-banner.jpg",
			"staff": {
				"edges": [
					{ "role": "Story", "node": { "name": { "full": "Tsugumi Ohba" } } },
					{ "role": "Art",   "node": { "name": { "full": "Takeshi Obata" } } }
				]
			}
		}
	}
}`

func TestSearchManga_TopMatch(t *testing.T) {
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: deathNoteMangaResp}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).SearchManga(context.Background(), "Death Note", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AniListID != 30014 {
		t.Errorf("AniListID = %d, want 30014", res.AniListID)
	}
	if res.MALID != 21 {
		t.Errorf("MALID = %d, want 21", res.MALID)
	}
	if res.Title != "Death Note" {
		t.Errorf("Title = %q, want Death Note", res.Title)
	}
	if res.Author != "Tsugumi Ohba" {
		t.Errorf("Author = %q, want Tsugumi Ohba", res.Author)
	}
	if res.Artist != "Takeshi Obata" {
		t.Errorf("Artist = %q, want Takeshi Obata", res.Artist)
	}
	if res.SerializationStatus != "FINISHED" {
		t.Errorf("Status = %q, want FINISHED", res.SerializationStatus)
	}
	if res.Volumes != 12 {
		t.Errorf("Volumes = %d, want 12 (FINISHED series)", res.Volumes)
	}
	if res.Chapters != 108 {
		t.Errorf("Chapters = %d, want 108", res.Chapters)
	}
	if res.ReadingDirection != "rtl" {
		t.Errorf("ReadingDirection = %q, want rtl (JP origin)", res.ReadingDirection)
	}
	if res.Rating != 8.5 {
		t.Errorf("Rating = %f, want 8.5", res.Rating)
	}
}

func TestSearchManga_OngoingHidesCounts(t *testing.T) {
	respBody := `{"data":{"Media":{
		"id": 1, "idMal": 1, "title": { "english": "Solo Leveling" },
		"format": "MANGA", "status": "RELEASING",
		"volumes": 0, "chapters": 200,
		"countryOfOrigin": "KR", "isAdult": false,
		"coverImage": { "extraLarge": "" }, "staff": { "edges": [] }
	}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).SearchManga(context.Background(), "Solo Leveling", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Volumes != -1 || res.Chapters != -1 {
		t.Errorf("RELEASING series should have -1 volumes/chapters, got %d/%d", res.Volumes, res.Chapters)
	}
	if res.ReadingDirection != "ttb" {
		t.Errorf("KR origin should map to ttb (webtoon), got %q", res.ReadingDirection)
	}
}

func TestSearchManga_StoryAndArtFusedRole(t *testing.T) {
	// Single creator with "Story & Art" role — same person fills both
	// Author and Artist slots. Common shounen / shoujo pattern.
	respBody := `{"data":{"Media":{
		"id": 1, "idMal": 1, "title": { "english": "One Punch Man" },
		"format": "MANGA", "status": "RELEASING",
		"volumes": 0, "chapters": 0,
		"countryOfOrigin": "JP", "isAdult": false,
		"coverImage": { "extraLarge": "" },
		"staff": { "edges": [
			{ "role": "Story & Art", "node": { "name": { "full": "ONE" } } }
		] }
	}}}`
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: respBody}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).SearchManga(context.Background(), "One Punch Man", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Author != "ONE" || res.Artist != "ONE" {
		t.Errorf("Story & Art role should fill both author and artist; got author=%q artist=%q",
			res.Author, res.Artist)
	}
}

func TestGetMangaByID(t *testing.T) {
	srv := httptest.NewServer((&graphqlEcho{t: t, respBody: deathNoteMangaResp}).handler())
	defer srv.Close()

	res, err := NewWithEndpoint(srv.URL).GetMangaByID(context.Background(), 30014)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.AniListID != 30014 {
		t.Errorf("AniListID = %d, want 30014", res.AniListID)
	}
}

// ── GetAnimeFranchise ───────────────────────────────────────────────────────
//
// franchiseRouter answers franchise-walk queries id-by-id. Each entry is
// the response body to return for {"id": N}. Anything unmapped 404s so a
// test that walks past its fixture fails loudly instead of looping or
// silently dropping nodes.
type franchiseRouter struct {
	t        *testing.T
	bodies   map[int]string
	requests []int
}

func (r *franchiseRouter) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var payload struct {
			Variables map[string]interface{} `json:"variables"`
		}
		_ = json.Unmarshal(body, &payload)
		idF, _ := payload.Variables["id"].(float64)
		id := int(idF)
		r.requests = append(r.requests, id)
		resp, ok := r.bodies[id]
		if !ok {
			r.t.Errorf("franchiseRouter: unmapped id %d in %q", id, string(body))
			http.Error(w, "unmapped id", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}
}

// franchiseNode builds a Media response body. relations is a list of
// (relationType, id, format, year, title) tuples — the full edge shape.
func franchiseNode(id, year int, title, format string, relations [][5]string) string {
	type edge struct {
		typ, nodeID, fmtVal, nodeYear, nodeTitle string
	}
	edges := make([]string, 0, len(relations))
	for _, r := range relations {
		edges = append(edges, `{
			"relationType": "`+r[0]+`",
			"node": {
				"id": `+r[1]+`,
				"idMal": 0,
				"format": "`+r[2]+`",
				"title": {"romaji": "`+r[4]+`", "english": "`+r[4]+`", "native": ""},
				"startDate": {"year": `+r[3]+`}
			}
		}`)
	}
	return `{"data":{"Media":{
		"id": ` + itoa(id) + `,
		"idMal": 0,
		"format": "` + format + `",
		"title": {"romaji": "` + title + `", "english": "` + title + `", "native": ""},
		"startDate": {"year": ` + itoa(year) + `},
		"relations": {"edges": [` + strings.Join(edges, ",") + `]}
	}}}`
}

func itoa(n int) string {
	// strconv.Itoa avoided to keep test imports minimal.
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestGetAnimeFranchise_LinearChain(t *testing.T) {
	// Three-season franchise: 1 → 2 → 3 via SEQUEL edges. Verify all
	// three rows come back in start-year order so callers can map
	// out[0]=S1, out[1]=S2, out[2]=S3.
	router := &franchiseRouter{t: t, bodies: map[int]string{
		1: franchiseNode(1, 2013, "Show S1", "TV", [][5]string{{"SEQUEL", "2", "TV", "2014", "Show S2"}}),
		2: franchiseNode(2, 2014, "Show S2", "TV", [][5]string{
			{"PREQUEL", "1", "TV", "2013", "Show S1"},
			{"SEQUEL", "3", "TV", "2015", "Show S3"},
		}),
		3: franchiseNode(3, 2015, "Show S3", "TV", [][5]string{{"PREQUEL", "2", "TV", "2014", "Show S2"}}),
	}}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	for i, want := range []int{1, 2, 3} {
		if out[i].AniListID != want {
			t.Errorf("out[%d].AniListID = %d, want %d", i, out[i].AniListID, want)
		}
	}
	if out[0].StartYear != 2013 || out[2].StartYear != 2015 {
		t.Errorf("year sort wrong: got %v", []int{out[0].StartYear, out[1].StartYear, out[2].StartYear})
	}
}

func TestGetAnimeFranchise_VisitedDedupBreaksCycle(t *testing.T) {
	// A → B → A loop. Without the visited map, the BFS would re-queue
	// A forever (or until depth cap). Both rows must appear exactly
	// once, and we expect exactly 2 HTTP requests.
	router := &franchiseRouter{t: t, bodies: map[int]string{
		1: franchiseNode(1, 2010, "A", "TV", [][5]string{{"SEQUEL", "2", "TV", "2011", "B"}}),
		2: franchiseNode(2, 2011, "B", "TV", [][5]string{{"SEQUEL", "1", "TV", "2010", "A"}}),
	}}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("len(out) = %d, want 2 (cycle should not duplicate rows)", len(out))
	}
	if len(router.requests) != 2 {
		t.Errorf("HTTP requests = %d, want 2 — visited map must short-circuit the cycle", len(router.requests))
	}
}

func TestGetAnimeFranchise_NonChainEdgesIgnored(t *testing.T) {
	// SIDE_STORY / SPIN_OFF / ALTERNATIVE / CHARACTER edges describe
	// related-but-not-same-franchise rows. They MUST NOT extend the
	// walk — including them would pollute season mapping with rows
	// that aren't part of the linear chain.
	router := &franchiseRouter{t: t, bodies: map[int]string{
		1: franchiseNode(1, 2010, "A", "TV", [][5]string{
			{"SIDE_STORY", "99", "TV", "2010", "Side"},
			{"SPIN_OFF", "98", "TV", "2010", "Spin"},
			{"ALTERNATIVE", "97", "TV", "2010", "Alt"},
			{"CHARACTER", "96", "TV", "2010", "Char"},
			{"SEQUEL", "2", "TV", "2011", "B"},
		}),
		2: franchiseNode(2, 2011, "B", "TV", nil),
	}}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	// Only id 1 + id 2 should appear — the SIDE_STORY / SPIN_OFF
	// neighbours must never be fetched. If they were, the router would
	// fail loudly because they aren't mapped.
	if len(out) != 2 {
		t.Errorf("len(out) = %d, want 2 — only PREQUEL/SEQUEL extend the chain", len(out))
	}
	for _, n := range out {
		if n.AniListID == 99 || n.AniListID == 98 || n.AniListID == 97 || n.AniListID == 96 {
			t.Errorf("non-chain neighbour leaked into result: %+v", n)
		}
	}
}

func TestGetAnimeFranchise_NonTVFormatsFiltered(t *testing.T) {
	// MOVIE / OVA / SPECIAL siblings aren't seasons in our model.
	// The walk must not descend into them via PREQUEL/SEQUEL edges,
	// even though those relation types are otherwise valid.
	router := &franchiseRouter{t: t, bodies: map[int]string{
		1: franchiseNode(1, 2010, "TV S1", "TV", [][5]string{
			{"SEQUEL", "50", "MOVIE", "2011", "Movie tie-in"},
			{"SEQUEL", "51", "OVA", "2011", "OVA tie-in"},
			{"SEQUEL", "52", "SPECIAL", "2011", "Special"},
			{"SEQUEL", "2", "TV", "2012", "TV S2"},
			{"SEQUEL", "3", "TV_SHORT", "2013", "TV Short S3"},
		}),
		2: franchiseNode(2, 2012, "TV S2", "TV", nil),
		3: franchiseNode(3, 2013, "TV Short S3", "TV_SHORT", nil),
	}}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	// 1, 2, 3 only — MOVIE/OVA/SPECIAL never fetched.
	if len(out) != 3 {
		t.Errorf("len(out) = %d, want 3 (TV + TV + TV_SHORT)", len(out))
	}
	for _, n := range out {
		if n.AniListID == 50 || n.AniListID == 51 || n.AniListID == 52 {
			t.Errorf("non-TV neighbour leaked into result: %+v (format %s)", n, n.Format)
		}
	}
}

func TestGetAnimeFranchise_DepthCapBoundsWalk(t *testing.T) {
	// Build a chain longer than maxFranchiseDepth (20) so the cap
	// kicks in. The exact stop point is implementation detail (the
	// BFS halts when len(visited) reaches the cap), but the result
	// must (a) be non-error, (b) not exceed the cap, and (c) start
	// from the input row.
	bodies := map[int]string{}
	for i := 1; i <= 30; i++ {
		var rels [][5]string
		if i < 30 {
			rels = [][5]string{{"SEQUEL", itoa(i + 1), "TV", itoa(2000 + i + 1), "S" + itoa(i+1)}}
		}
		bodies[i] = franchiseNode(i, 2000+i, "S"+itoa(i), "TV", rels)
	}
	router := &franchiseRouter{t: t, bodies: bodies}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	if len(out) > maxFranchiseDepth {
		t.Errorf("len(out) = %d, exceeds maxFranchiseDepth=%d", len(out), maxFranchiseDepth)
	}
	if len(out) == 0 || out[0].AniListID != 1 {
		t.Errorf("first row should be the input id; got %+v", out)
	}
}

func TestGetAnimeFranchise_TitleFallbackEnglishOverRomaji(t *testing.T) {
	// English title preferred when both present. The franchise walk
	// reuses anilistTitleNode.bestTitle() — verify the contract holds
	// at the franchise output level.
	body := `{"data":{"Media":{
		"id": 7, "idMal": 0, "format": "TV",
		"title": {"romaji": "Shingeki no Kyojin", "english": "Attack on Titan", "native": "進撃の巨人"},
		"startDate": {"year": 2013},
		"relations": {"edges": []}
	}}}`
	router := &franchiseRouter{t: t, bodies: map[int]string{7: body}}
	srv := httptest.NewServer(router.handler())
	defer srv.Close()

	out, err := NewWithEndpoint(srv.URL).GetAnimeFranchise(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetAnimeFranchise: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].Title != "Attack on Titan" {
		t.Errorf("Title = %q, want %q (english should beat romaji)", out[0].Title, "Attack on Titan")
	}
}

func TestStripHTML_HandlesCommonEntities(t *testing.T) {
	// Tags get stripped + nbsp normalised to a regular space; HTML
	// entities are intentionally left ENCODED so a future {@html ...}
	// renderer can't be fed XSS via a malicious AniList description
	// that contains literal "&lt;script&gt;" text. See stripHTML doc
	// comment for the full rationale.
	cases := map[string]string{
		"<p>Hello</p>":                            "Hello",
		"Foo<br>Bar":                              "FooBar",
		"a&nbsp;b":                                "a b",
		"&amp; &lt;tag&gt;":                       "&amp; &lt;tag&gt;",
		"<i>It&#39;s an &quot;example&quot;</i>":  `It&#39;s an &quot;example&quot;`,
		"  leading + trailing  ":                  "leading + trailing",
	}
	for in, want := range cases {
		if got := stripHTML(in); got != want {
			t.Errorf("stripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}
