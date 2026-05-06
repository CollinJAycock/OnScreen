package animedb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeTitle(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii lowercased", "Cowboy Bebop", "cowboy bebop"},
		{"strips trailing exclamation", "Akame ga Kill!", "akame ga kill"},
		{"strips colon subtitle separator", "Akame ga Kill! Gaiden: Theater", "akame ga kill gaiden theater"},
		{"strips diacritics", "Pokémon", "pokemon"},
		{"normalizes full-width", "ＡＫＩＲＡ", "akira"},
		{"collapses whitespace runs", "  Steins;Gate    0  ", "steins gate 0"},
		{"strips punctuation_collapses", "Re:Zero - Starting Life in Another World", "re zero starting life in another world"},
		{"empty stays empty", "", ""},
		{"all punctuation becomes empty", "!!!", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeTitle(c.in)
			if got != c.want {
				t.Errorf("normalizeTitle(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestParseTrailingID(t *testing.T) {
	cases := []struct {
		src    string
		prefix string
		want   int
	}{
		{"https://anilist.co/anime/1/", "https://anilist.co/anime/", 1},
		{"https://anilist.co/anime/12345", "https://anilist.co/anime/", 12345},
		{"https://anilist.co/anime/12345/extra", "https://anilist.co/anime/", 12345},
		{"https://kitsu.io/anime/cowboy-bebop", "https://kitsu.io/anime/", 0},
		{"https://anilist.co/anime/", "https://anilist.co/anime/", 0},
	}
	for _, c := range cases {
		got := parseTrailingID(c.src, c.prefix)
		if got != c.want {
			t.Errorf("parseTrailingID(%q) = %d, want %d", c.src, got, c.want)
		}
	}
}

// fixture builds a small dataset that exercises the realistic
// matching paths: fansub-style synonym, punctuation drift, and
// distinct entries sharing a normalized prefix.
func fixture(t *testing.T) string {
	t.Helper()
	dataset := rawFile{
		Data: []rawEntry{
			{
				Title: "Akame ga Kill!",
				Type:  "TV",
				Sources: []string{
					"https://anilist.co/anime/20613/",
					"https://myanimelist.net/anime/22199/",
				},
				Synonyms: []string{"Akame ga Kiru!", "Red Eyes Sword"},
				AnimeSeason: struct {
					Year int `json:"year"`
				}{Year: 2014},
			},
			{
				Title: "Akame ga Kill! Gaiden: Theater",
				Type:  "ONA",
				Sources: []string{
					"https://anilist.co/anime/20988/",
				},
				// The fansub-style folder name a user is likely to have
				// on disk — this is precisely the case AniList live
				// search misses and the offline DB recovers.
				Synonyms: []string{"Akame ga Kill Theater", "Theater"},
				AnimeSeason: struct {
					Year int `json:"year"`
				}{Year: 2014},
			},
			{
				Title: "Pokémon",
				Type:  "TV",
				Sources: []string{
					"https://anilist.co/anime/527/",
				},
				Synonyms: []string{"Pokemon", "Pocket Monsters"},
				AnimeSeason: struct {
					Year int `json:"year"`
				}{Year: 1997},
			},
		},
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "fixture.json")
	f, err := os.Create(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(dataset); err != nil {
		t.Fatal(err)
	}
	return srcPath
}

func newOpenedDB(t *testing.T) *DB {
	t.Helper()
	srcPath := fixture(t)
	cacheDir := t.TempDir()
	db := NewWithSource(cacheDir, "file://"+srcPath, nil, nil)
	if err := db.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db
}

func TestLookup_PrimaryTitleHits(t *testing.T) {
	db := newOpenedDB(t)
	got, ok := db.Lookup("Akame ga Kill!")
	if !ok {
		t.Fatal("expected hit on primary title")
	}
	if got.AniListID != 20613 {
		t.Errorf("AniListID = %d, want 20613", got.AniListID)
	}
}

func TestLookup_FansubSynonymHits(t *testing.T) {
	// The headline case: folder is "Akame ga Kill Theater" (no
	// "Gaiden:"), AniList live search would miss, but manami's
	// synonym list catches it.
	db := newOpenedDB(t)
	got, ok := db.Lookup("Akame ga Kill Theater")
	if !ok {
		t.Fatal("expected fansub synonym hit")
	}
	if got.AniListID != 20988 {
		t.Errorf("AniListID = %d, want 20988 (Gaiden: Theater entry)", got.AniListID)
	}
}

func TestLookup_PunctuationVariantHits(t *testing.T) {
	db := newOpenedDB(t)
	for _, q := range []string{"akame ga kill", "Akame Ga Kill", "AKAME GA KILL"} {
		got, ok := db.Lookup(q)
		if !ok {
			t.Errorf("%q: expected hit", q)
			continue
		}
		if got.AniListID != 20613 {
			t.Errorf("%q: AniListID = %d, want 20613", q, got.AniListID)
		}
	}
}

func TestLookup_DiacriticsFold(t *testing.T) {
	db := newOpenedDB(t)
	for _, q := range []string{"Pokemon", "Pokémon", "pokemon"} {
		got, ok := db.Lookup(q)
		if !ok {
			t.Errorf("%q: expected diacritic-folded hit", q)
			continue
		}
		if got.AniListID != 527 {
			t.Errorf("%q: AniListID = %d, want 527", q, got.AniListID)
		}
	}
}

func TestLookup_NoMatchReturnsFalse(t *testing.T) {
	db := newOpenedDB(t)
	if _, ok := db.Lookup("Some Show That Does Not Exist"); ok {
		t.Error("expected miss")
	}
	if _, ok := db.Lookup(""); ok {
		t.Error("empty title should miss")
	}
}

func TestLookupByAniListID(t *testing.T) {
	db := newOpenedDB(t)
	got, ok := db.LookupByAniListID(20988)
	if !ok {
		t.Fatal("expected by-id hit")
	}
	if got.Title != "Akame ga Kill! Gaiden: Theater" {
		t.Errorf("Title = %q", got.Title)
	}
	if _, ok := db.LookupByAniListID(0); ok {
		t.Error("id=0 should miss")
	}
	if _, ok := db.LookupByAniListID(999999); ok {
		t.Error("unknown id should miss")
	}
}

func TestSize(t *testing.T) {
	db := newOpenedDB(t)
	if db.Size() != 3 {
		t.Errorf("Size() = %d, want 3", db.Size())
	}
}

// TestOpen_RefreshUsesCachedFallback verifies that a fetch failure
// after the initial cache exists doesn't tear the DB down — the
// stale local file gets used while the operator's monitoring picks
// up the WARN log.
func TestOpen_RefreshUsesCachedFallback(t *testing.T) {
	cacheDir := t.TempDir()
	// Pre-seed the cache with valid data.
	preseed := rawFile{Data: []rawEntry{{
		Title:   "Cowboy Bebop",
		Type:    "TV",
		Sources: []string{"https://anilist.co/anime/1/"},
	}}}
	cachePath := filepath.Join(cacheDir, "anime-offline-database.json")
	f, _ := os.Create(cachePath)
	_ = json.NewEncoder(f).Encode(preseed)
	f.Close()
	// Mark the cache as stale (older than CacheTTL) so Open tries
	// to refresh and falls back when the server fails.
	if err := os.Chtimes(cachePath, time.Now().Add(-30*24*time.Hour), time.Now().Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	// Source returns 500 — refresh fails. Open should still load
	// from the stale cache instead of erroring out.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := NewWithSource(cacheDir, srv.URL, srv.Client(), nil)
	if err := db.Open(context.Background()); err != nil {
		t.Fatalf("Open should not have erred with cached fallback: %v", err)
	}
	if _, ok := db.Lookup("Cowboy Bebop"); !ok {
		t.Error("stale cache should have been loaded")
	}
}
