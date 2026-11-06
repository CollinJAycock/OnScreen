//go:build integration

// These tests verify the search SQL itself against a real Postgres
// (via testcontainers). They are gated by the `integration` build tag
// so the default `go test ./...` run stays fast and Docker-free.
//
// Run with: go test -tags=integration ./internal/api/v1/...
package v1

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/db/gen"
	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/testdb"
)

func seedSearchCorpus(ctx context.Context, t *testing.T, q *gen.Queries, libraryID uuid.UUID, items map[string]string) map[string]uuid.UUID {
	t.Helper()
	ids := make(map[string]uuid.UUID, len(items))
	for title, originalTitle := range items {
		var origPtr *string
		if originalTitle != "" {
			ot := originalTitle
			origPtr = &ot
		}
		row, err := q.CreateMediaItem(ctx, gen.CreateMediaItemParams{
			LibraryID:     libraryID,
			Type:          "movie",
			Title:         title,
			SortTitle:     title,
			OriginalTitle: origPtr,
		})
		if err != nil {
			t.Fatalf("seed %q: %v", title, err)
		}
		ids[title] = row.ID
	}
	return ids
}

func mustCreateLibrary(ctx context.Context, t *testing.T, q *gen.Queries) uuid.UUID {
	t.Helper()
	lib, err := q.CreateLibrary(ctx, gen.CreateLibraryParams{
		Name:                    "test-" + uuid.New().String()[:8],
		Type:                    "movie",
		ScanPaths:               []string{"/tmp"},
		Agent:                   "tmdb",
		Language:                "en",
		ScanInterval:            time.Hour,
		MetadataRefreshInterval: 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("create library: %v", err)
	}
	return lib.ID
}

// TestSearch_Integration_FuzzyMatch confirms pg_trgm catches typos that
// FTS alone cannot — "matrx" finds "The Matrix".
func TestSearch_Integration_FuzzyMatch(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	ids := seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"The Matrix":   "",
		"Inception":    "",
		"Interstellar": "",
	})

	rows, err := q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:  libID,
		Query:      "matrx", // typo
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("SearchMediaItems: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("typo search returned no results — pg_trgm fallback not triggering")
	}
	if rows[0].ID != ids["The Matrix"] {
		t.Errorf("top result for 'matrx': got %q, want 'The Matrix'", rows[0].Title)
	}
}

// TestSearch_Integration_ForeignTitle confirms trigram fallback matches
// against original_title when the english stemmer would miss it.
func TestSearch_Integration_ForeignTitle(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	ids := seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"Amélie":          "Le Fabuleux Destin d'Amélie Poulain",
		"Pan's Labyrinth": "El Laberinto del Fauno",
		"Crouching Tiger": "臥虎藏龍",
	})

	rows, err := q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:  libID,
		Query:      "fabuleux",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("SearchMediaItems: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("foreign-title search returned no results")
	}
	if rows[0].ID != ids["Amélie"] {
		t.Errorf("top result for 'fabuleux': got %q, want 'Amélie'", rows[0].Title)
	}
}

// TestSearch_Integration_WebsearchSyntax confirms websearch_to_tsquery
// accepts quoted phrases — plainto_tsquery does not.
func TestSearch_Integration_WebsearchSyntax(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	ids := seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"The Dark Knight": "",
		"Knight and Day":  "",
		"A Knight's Tale": "",
	})

	rows, err := q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:  libID,
		Query:      `"dark knight"`,
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("SearchMediaItems: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("phrase search returned no results")
	}
	if rows[0].ID != ids["The Dark Knight"] {
		t.Errorf("top result for '\"dark knight\"': got %q, want 'The Dark Knight'", rows[0].Title)
	}
}

// TestSearch_Integration_ExactStillRanksFirst confirms the GREATEST()
// ranking doesn't let a fuzzy match outrank an exact lexical hit.
func TestSearch_Integration_ExactStillRanksFirst(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	ids := seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"Alien":     "",
		"Aliens":    "",
		"Alienator": "",
	})

	rows, err := q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:  libID,
		Query:      "alien",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("SearchMediaItems: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("exact search returned no results")
	}
	if rows[0].ID != ids["Alien"] {
		t.Errorf("top result for 'alien': got %q, want 'Alien' (exact must beat fuzzy)", rows[0].Title)
	}
}

// TestSearch_Integration_GlobalRespectsAllLibraries confirms the global
// variant returns hits across libraries.
func TestSearch_Integration_GlobalRespectsAllLibraries(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	lib1 := mustCreateLibrary(ctx, t, q)
	lib2 := mustCreateLibrary(ctx, t, q)
	seedSearchCorpus(ctx, t, q, lib1, map[string]string{"Inception": ""})
	seedSearchCorpus(ctx, t, q, lib2, map[string]string{"Inception 2": ""})

	rows, err := q.SearchMediaItemsGlobal(ctx, gen.SearchMediaItemsGlobalParams{
		Query:      "inception",
		MaxResults: 10,
	})
	if err != nil {
		t.Fatalf("SearchMediaItemsGlobal: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("global search: got %d results, want 2 (one per library)", len(rows))
	}
}

// searchTitles runs SearchMediaItems the way the handler does (with the
// prefix query) and returns the titles in rank order.
func searchTitles(ctx context.Context, t *testing.T, q *gen.Queries, libID uuid.UUID, query string) []string {
	t.Helper()
	rows, err := q.SearchMediaItems(ctx, gen.SearchMediaItemsParams{
		LibraryID:   libID,
		Query:       query,
		PrefixQuery: media.PrefixTSQuery(query),
		MaxResults:  20,
	})
	if err != nil {
		t.Fatalf("SearchMediaItems(%q): %v", query, err)
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Title
	}
	return out
}

func containsTitle(titles []string, want string) bool {
	for _, t := range titles {
		if t == want {
			return true
		}
	}
	return false
}

// TestSearch_Integration_NotOnlyExactTitles covers the user report that
// search "needs a really exact title": a word still being typed, a short
// query against a long title, a word from the middle of a title, and an
// unaccented query for an accented title all find it.
func TestSearch_Integration_NotOnlyExactTitles(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"Avatar: The Way of Water":              "",
		"Star Wars: The Empire Strikes Back":    "",
		"Amélie":                                "",
		"The Lord of the Rings: The Two Towers": "",
		"Inception":                             "",
	})

	for _, c := range []struct{ query, want string }{
		{"avat", "Avatar: The Way of Water"},                           // a word still being typed
		{"way of wat", "Avatar: The Way of Water"},                     // the last word half typed
		{"star", "Star Wars: The Empire Strikes Back"},                 // short query, long title
		{"empire", "Star Wars: The Empire Strikes Back"},               // a word from the middle
		{"two towers", "The Lord of the Rings: The Two Towers"},        // the subtitle alone
		{"amelie", "Amélie"},                                           // no accent typed
		{"lord of the rinsg", "The Lord of the Rings: The Two Towers"}, // a typo in a long title
	} {
		got := searchTitles(ctx, t, q, libID, c.query)
		if len(got) == 0 || got[0] != c.want {
			t.Errorf("search %q: got %v, want %q first", c.query, got, c.want)
		}
	}
}

// TestSearch_Integration_TitleRanking: an exact title, then titles starting
// with the query, then titles containing it.
func TestSearch_Integration_TitleRanking(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"Night":                "",
		"Night of the Comet":   "",
		"A Night at the Opera": "",
	})
	got := searchTitles(ctx, t, q, libID, "night")
	want := []string{"Night", "Night of the Comet", "A Night at the Opera"}
	if len(got) < 3 {
		t.Fatalf("search night: got %v, want at least %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("search night: rank %d = %q, want %q (all: %v)", i, got[i], w, got)
		}
	}
}

// TestSearch_Integration_LikeWildcardsAreLiteral: % and _ in a query are
// characters to find, not LIKE wildcards matching every title.
func TestSearch_Integration_LikeWildcardsAreLiteral(t *testing.T) {
	pool := testdb.New(t)
	q := gen.New(pool)
	ctx := context.Background()

	libID := mustCreateLibrary(ctx, t, q)
	seedSearchCorpus(ctx, t, q, libID, map[string]string{
		"100% Wolf": "",
		"Inception": "",
		"Spirited":  "",
	})
	if got := searchTitles(ctx, t, q, libID, "%"); containsTitle(got, "Inception") || containsTitle(got, "Spirited") {
		t.Errorf("search %%: got %v, a wildcard matched every title", got)
	}
	if got := searchTitles(ctx, t, q, libID, "100%"); len(got) == 0 || got[0] != "100% Wolf" {
		t.Errorf("search 100%%: got %v, want 100%% Wolf first", got)
	}
	if got := searchTitles(ctx, t, q, libID, "_"); containsTitle(got, "Inception") {
		t.Errorf("search _: got %v, a wildcard matched every title", got)
	}
}
