package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onscreen/onscreen/internal/metadata"
)

func TestLang(t *testing.T) {
	c := New("k", 1, "")
	cases := map[string]string{
		"":      "en-US",
		"en":    "en-US",
		"fr":    "fr-FR",
		"ZH":    "zh-CN",
		"pt-PT": "pt-PT",
		"fi":    "fi",
	}
	for in, want := range cases {
		if got := c.lang(metadata.WithLanguage(context.Background(), in)); got != want {
			t.Errorf("lang(%q) = %q, want %q", in, got, want)
		}
	}
}

// A library language reaches the details call, while the search that finds
// the movie stays in the client's language so auto-match sees the same
// candidates.
func TestLibraryLanguage_DetailsLocalizedSearchNot(t *testing.T) {
	var mu sync.Mutex
	langs := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		langs[r.URL.Path] = r.URL.Query().Get("language")
		mu.Unlock()
		switch r.URL.Path {
		case "/3/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
				{"id": 194, "title": "Amélie", "original_title": "Le Fabuleux Destin d'Amélie Poulain", "release_date": "2001-04-25"},
			}})
		default:
			json.NewEncoder(w).Encode(map[string]any{
				"id": 194, "title": "Le Fabuleux Destin d'Amélie Poulain",
				"release_date": "2001-04-25", "overview": "Amélie est une jeune serveuse…",
			})
		}
	}))
	defer srv.Close()

	c := testClient(t, srv)
	ctx := metadata.WithLanguage(context.Background(), "fr")
	res, err := c.SearchMovie(ctx, "Amélie", 2001)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "Amélie est une jeune serveuse…" {
		t.Errorf("summary = %q", res.Summary)
	}
	if got := langs["/3/search/movie"]; got != "en-US" {
		t.Errorf("search language = %q, want en-US", got)
	}
	if got := langs["/3/movie/194"]; got != "fr-FR" {
		t.Errorf("details language = %q, want fr-FR", got)
	}
}

// TMDB answers an untranslated overview with "": the default language fills
// it, and the localized title stays.
func TestLibraryLanguage_FallsBackForMissingOverview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "de-DE" {
			json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "Die Show", "overview": ""})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "The Show", "overview": "English overview"})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	res, err := c.RefreshTV(metadata.WithLanguage(context.Background(), "de"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Die Show" || res.Summary != "English overview" {
		t.Errorf("got title %q summary %q", res.Title, res.Summary)
	}
}

func TestLibraryLanguage_SeasonFillsEpisodeGaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fr := r.URL.Query().Get("language") == "fr-FR"
		ep := func(n int, frName, frOv, enOv string) map[string]any {
			if fr {
				return map[string]any{"episode_number": n, "name": frName, "overview": frOv}
			}
			return map[string]any{"episode_number": n, "name": "Episode", "overview": enOv}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"season_number": 1, "overview": map[bool]string{true: "Saison", false: "Season"}[fr],
			"episodes": []map[string]any{
				ep(1, "Pilote", "Résumé un", "Summary one"),
				ep(2, "Épisode 2", "", "Summary two"),
			},
		})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	s, err := c.GetSeason(metadata.WithLanguage(context.Background(), "fr"), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s.Summary != "Saison" {
		t.Errorf("season summary = %q", s.Summary)
	}
	if s.Episodes[0].Summary != "Résumé un" || s.Episodes[1].Summary != "Summary two" {
		t.Errorf("episode summaries = %q, %q", s.Episodes[0].Summary, s.Episodes[1].Summary)
	}
	if s.Episodes[1].Title != "Épisode 2" {
		t.Errorf("episode 2 title = %q, want the localized one", s.Episodes[1].Title)
	}
}

// No library language: one request, exactly as before.
func TestNoLibraryLanguage_NoFallbackRequest(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/tv/1" {
			n++
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "The Show", "overview": ""})
	}))
	defer srv.Close()

	c := testClient(t, srv)
	if _, err := c.RefreshTV(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("details requests = %d, want 1", n)
	}
}
