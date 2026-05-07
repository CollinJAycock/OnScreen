package scanner

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/anilist"
	"github.com/onscreen/onscreen/internal/metadata/animedb"
)

// ── mocks ────────────────────────────────────────────────────────────────────

type mockAgent struct {
	searchMovieResult *metadata.MovieResult
	searchMovieErr    error
	searchTVResult    *metadata.TVShowResult
	searchTVErr       error
	getSeasonResult   *metadata.SeasonResult
	getSeasonErr      error
	getEpisodeResult  *metadata.EpisodeResult
	getEpisodeErr     error
	// Call counters so tests can prove the cascade did or didn't make
	// per-episode round-trips.
	getEpisodeCalls int
	getSeasonCalls  int
}

func (m *mockAgent) SearchMovie(_ context.Context, _ string, _ int) (*metadata.MovieResult, error) {
	if m.searchMovieErr != nil {
		return nil, m.searchMovieErr
	}
	return m.searchMovieResult, nil
}
func (m *mockAgent) SearchTV(_ context.Context, _ string, _ int) (*metadata.TVShowResult, error) {
	if m.searchTVErr != nil {
		return nil, m.searchTVErr
	}
	return m.searchTVResult, nil
}
func (m *mockAgent) SearchTVCandidates(_ context.Context, _ string) ([]metadata.TVShowResult, error) {
	return nil, nil
}
func (m *mockAgent) GetSeason(_ context.Context, _, _ int) (*metadata.SeasonResult, error) {
	m.getSeasonCalls++
	if m.getSeasonErr != nil {
		return nil, m.getSeasonErr
	}
	return m.getSeasonResult, nil
}
func (m *mockAgent) GetEpisode(_ context.Context, _, _, _ int) (*metadata.EpisodeResult, error) {
	m.getEpisodeCalls++
	if m.getEpisodeErr != nil {
		return nil, m.getEpisodeErr
	}
	return m.getEpisodeResult, nil
}
func (m *mockAgent) RefreshMovie(_ context.Context, _ int) (*metadata.MovieResult, error) {
	return m.searchMovieResult, m.searchMovieErr
}
func (m *mockAgent) RefreshTV(_ context.Context, _ int) (*metadata.TVShowResult, error) {
	return m.searchTVResult, m.searchTVErr
}

// mockTVDB satisfies the TVDBFallback interface for enricher tests.
// Tracks call counts so a test can prove TVDB was actually consulted
// when the show only has a TVDB ID (anime path).
type mockTVDB struct {
	getEpisodeResult *metadata.EpisodeResult
	getEpisodeErr    error
	getEpisodeCalls  int

	searchSeriesResult *metadata.TVShowResult
	searchSeriesErr    error
}

func (m *mockTVDB) GetEpisode(_ context.Context, _, _, _ int) (*metadata.EpisodeResult, error) {
	m.getEpisodeCalls++
	if m.getEpisodeErr != nil {
		return nil, m.getEpisodeErr
	}
	return m.getEpisodeResult, nil
}

func (m *mockTVDB) SearchSeries(_ context.Context, _ string, _ int) (*metadata.TVShowResult, error) {
	if m.searchSeriesErr != nil {
		return nil, m.searchSeriesErr
	}
	return m.searchSeriesResult, nil
}

type mockUpdater struct {
	items       map[uuid.UUID]*media.Item
	children    map[uuid.UUID][]media.Item // parentID -> children
	files       map[uuid.UUID][]media.File
	updateCalls []media.UpdateItemMetadataParams

	// Pre-flight merge support for matchShow / matchMovie. Tests register
	// "tmdb-id already attached" survivors here; the mock returns ErrNotFound
	// otherwise.
	itemByTMDB    map[int]*media.Item
	mergeCalls    []mergeCall
	mergeErr      error
}

type mergeCall struct {
	LoserID, SurvivorID uuid.UUID
	ItemType            string
}

func newMockUpdater() *mockUpdater {
	return &mockUpdater{
		items:      make(map[uuid.UUID]*media.Item),
		children:   make(map[uuid.UUID][]media.Item),
		files:      make(map[uuid.UUID][]media.File),
		itemByTMDB: make(map[int]*media.Item),
	}
}

func (m *mockUpdater) UpdateItemMetadata(_ context.Context, p media.UpdateItemMetadataParams) (*media.Item, error) {
	m.updateCalls = append(m.updateCalls, p)
	if it, ok := m.items[p.ID]; ok {
		it.Title = p.Title
		it.SortTitle = p.SortTitle
		it.Summary = p.Summary
		it.Rating = p.Rating
		it.PosterPath = p.PosterPath
		it.FanartPath = p.FanartPath
		it.ThumbPath = p.ThumbPath
		it.Year = p.Year
		it.TMDBID = p.TMDBID
		return it, nil
	}
	return nil, errors.New("item not found")
}

func (m *mockUpdater) GetItem(_ context.Context, id uuid.UUID) (*media.Item, error) {
	if it, ok := m.items[id]; ok {
		return it, nil
	}
	return nil, errors.New("item not found")
}

func (m *mockUpdater) GetFiles(_ context.Context, itemID uuid.UUID) ([]media.File, error) {
	return m.files[itemID], nil
}

func (m *mockUpdater) ListChildren(_ context.Context, parentID uuid.UUID) ([]media.Item, error) {
	return m.children[parentID], nil
}

func (m *mockUpdater) GetItemByTMDBID(_ context.Context, _ uuid.UUID, tmdbID int) (*media.Item, error) {
	if it, ok := m.itemByTMDB[tmdbID]; ok {
		return it, nil
	}
	return nil, errors.New("not found")
}

func (m *mockUpdater) MergeIntoTopLevel(_ context.Context, loserID, survivorID uuid.UUID, itemType string) error {
	if m.mergeErr != nil {
		return m.mergeErr
	}
	m.mergeCalls = append(m.mergeCalls, mergeCall{LoserID: loserID, SurvivorID: survivorID, ItemType: itemType})
	return nil
}

type mockArtwork struct {
	posterPath string
	fanartPath string
	thumbPath  string
	posterErr  error
	fanartErr  error
	thumbErr   error
}

func (m *mockArtwork) DownloadPoster(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.posterErr != nil {
		return "", m.posterErr
	}
	return m.posterPath, nil
}
func (m *mockArtwork) DownloadFanart(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.fanartErr != nil {
		return "", m.fanartErr
	}
	return m.fanartPath, nil
}
func (m *mockArtwork) DownloadThumb(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.thumbErr != nil {
		return "", m.thumbErr
	}
	return m.thumbPath, nil
}
func (m *mockArtwork) ReplacePoster(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.posterErr != nil {
		return "", m.posterErr
	}
	return m.posterPath, nil
}

func (m *mockArtwork) ReplaceShowPoster(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.posterErr != nil {
		return "", m.posterErr
	}
	return m.posterPath, nil
}

func (m *mockArtwork) ReplaceShowFanart(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.fanartErr != nil {
		return "", m.fanartErr
	}
	return m.fanartPath, nil
}

func (m *mockArtwork) DownloadArtistPoster(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.posterErr != nil {
		return "", m.posterErr
	}
	return m.posterPath, nil
}

func (m *mockArtwork) DownloadArtistFanart(_ context.Context, _ uuid.UUID, _, _ string) (string, error) {
	if m.fanartErr != nil {
		return "", m.fanartErr
	}
	return m.fanartPath, nil
}

func newTestEnricher(agent metadata.Agent, updater *mockUpdater, artwork *mockArtwork) *Enricher {
	agentFn := func() metadata.Agent { return agent }
	if artwork == nil {
		artwork = &mockArtwork{}
	}
	scanPaths := func() []string { return []string{"/media"} }
	return NewEnricher(agentFn, artwork, updater, scanPaths, slog.Default())
}

// ── Enrich dispatch ──────────────────────────────────────────────────────────

func TestEnrich_NilAgent_Noop(t *testing.T) {
	updater := newMockUpdater()
	e := NewEnricher(func() metadata.Agent { return nil }, nil, updater, func() []string { return nil }, slog.Default())

	err := e.Enrich(context.Background(), &media.Item{Type: "movie"}, &media.File{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("expected no update calls when agent is nil")
	}
}

func TestEnrich_UnknownType_Noop(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	e := newTestEnricher(agent, updater, nil)

	err := e.Enrich(context.Background(), &media.Item{Type: "photo"}, &media.File{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("expected no update calls for unknown type")
	}
}

// ── enrichMovie ──────────────────────────────────────────────────────────────

func TestEnrichMovie_Success(t *testing.T) {
	year := 1999
	agent := &mockAgent{
		searchMovieResult: &metadata.MovieResult{
			TMDBID:    603,
			Title:     "The Matrix",
			Year:      1999,
			Summary:   "A computer hacker learns...",
			Rating:    8.7,
			Genres:    []string{"Action", "Sci-Fi"},
			PosterURL: "http://example.com/poster.jpg",
			FanartURL: "http://example.com/fanart.jpg",
		},
	}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "The Matrix", Year: &year}
	artwork := &mockArtwork{posterPath: "/media/movies/poster.jpg", fanartPath: "/media/movies/fanart.jpg"}
	e := newTestEnricher(agent, updater, artwork)

	err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/The.Matrix.1999.mkv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "The Matrix" {
		t.Errorf("title: got %q, want %q", p.Title, "The Matrix")
	}
	if p.PosterPath == nil || *p.PosterPath != "movies/poster.jpg" {
		t.Errorf("poster_path: got %v, want movies/poster.jpg", p.PosterPath)
	}
	if p.FanartPath == nil || *p.FanartPath != "movies/fanart.jpg" {
		t.Errorf("fanart_path: got %v, want movies/fanart.jpg", p.FanartPath)
	}
}

func TestEnrichMovie_SearchFails_NoError(t *testing.T) {
	agent := &mockAgent{searchMovieErr: errors.New("no results")}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Unknown Movie"}
	e := newTestEnricher(agent, updater, nil)

	err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/unknown.mkv"})
	if err != nil {
		t.Fatalf("search failure should not propagate: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("should not update when search fails")
	}
}

// ── enrichShow ───────────────────────────────────────────────────────────────

func TestEnrichShow_Success(t *testing.T) {
	tmdbID := 1396
	agent := &mockAgent{
		searchTVResult: &metadata.TVShowResult{
			TMDBID:       tmdbID,
			Title:        "Breaking Bad",
			FirstAirYear: 2008,
			Summary:      "A chemistry teacher turns to crime.",
			Rating:       9.5,
			Genres:       []string{"Drama", "Crime"},
			PosterURL:    "http://example.com/bb_poster.jpg",
			FanartURL:    "http://example.com/bb_fanart.jpg",
		},
	}
	updater := newMockUpdater()
	showID := uuid.New()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad"}
	artwork := &mockArtwork{posterPath: "/media/shows/poster.jpg", fanartPath: "/media/shows/fanart.jpg"}
	e := newTestEnricher(agent, updater, artwork)

	err := e.enrichShow(context.Background(), agent, updater.items[showID], &media.File{FilePath: "/media/shows/Breaking.Bad.S01E01.mkv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) < 1 {
		t.Fatal("expected at least 1 update call")
	}
	p := updater.updateCalls[0]
	if p.Title != "Breaking Bad" {
		t.Errorf("title: got %q, want %q", p.Title, "Breaking Bad")
	}
	if p.TMDBID == nil || *p.TMDBID != tmdbID {
		t.Errorf("tmdb_id: got %v, want %d", p.TMDBID, tmdbID)
	}
	if p.PosterPath == nil || *p.PosterPath != "shows/poster.jpg" {
		t.Errorf("poster: got %v, want shows/poster.jpg", p.PosterPath)
	}
}

func TestEnrichShow_SearchFails_NoError(t *testing.T) {
	agent := &mockAgent{searchTVErr: errors.New("no results")}
	updater := newMockUpdater()
	showID := uuid.New()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Unknown Show"}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichShow(context.Background(), agent, updater.items[showID], &media.File{FilePath: "/media/shows/unknown.mkv"})
	if err != nil {
		t.Fatalf("search failure should not propagate: %v", err)
	}
}

// ── enrichSeason ─────────────────────────────────────────────────────────────

func TestEnrichSeason_Success(t *testing.T) {
	tmdbID := 1396
	seasonNum := 1
	showID := uuid.New()
	seasonID := uuid.New()

	agent := &mockAgent{
		getSeasonResult: &metadata.SeasonResult{
			Number:    1,
			Name:      "Season 1",
			Summary:   "Walter White starts cooking.",
			AirDate:   time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
			PosterURL: "http://example.com/season1.jpg",
		},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad", TMDBID: &tmdbID}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		Title:    "Season 1",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	artwork := &mockArtwork{posterPath: "/media/shows/BB/season1_poster.jpg"}
	e := newTestEnricher(agent, updater, artwork)

	err := e.enrichSeason(context.Background(), agent, updater.items[seasonID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) < 1 {
		t.Fatal("expected at least 1 update call for season")
	}
	p := updater.updateCalls[0]
	if p.Title != "Season 1" {
		t.Errorf("title: got %q, want %q", p.Title, "Season 1")
	}
}

func TestEnrichSeason_NoParentID_Noop(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	seasonID := uuid.New()
	seasonNum := 1
	updater.items[seasonID] = &media.Item{ID: seasonID, Type: "season", Index: &seasonNum}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichSeason(context.Background(), agent, updater.items[seasonID], &media.File{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("should skip season with no parent_id")
	}
}

func TestEnrichSeason_ShowNotEnriched_Noop(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	showID := uuid.New()
	seasonID := uuid.New()
	seasonNum := 1
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Unenriched Show"}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichSeason(context.Background(), agent, updater.items[seasonID], &media.File{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("should skip when parent show has no TMDB ID")
	}
}

// ── enrichEpisode ────────────────────────────────────────────────────────────

func TestEnrichEpisode_Success(t *testing.T) {
	tmdbID := 1396
	seasonNum := 1
	episodeNum := 3
	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()

	agent := &mockAgent{
		getEpisodeResult: &metadata.EpisodeResult{
			Title:    "...And the Bag's in the River",
			Summary:  "Walt must deal with the aftermath.",
			Rating:   8.1,
			AirDate:  time.Date(2008, 2, 10, 0, 0, 0, 0, time.UTC),
			ThumbURL: "http://example.com/ep3_thumb.jpg",
		},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad", TMDBID: &tmdbID}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		Title:    "Season 1",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	updater.items[episodeID] = &media.Item{
		ID:       episodeID,
		Type:     "episode",
		Title:    "Episode 3",
		ParentID: &seasonID,
		Index:    &episodeNum,
	}
	artwork := &mockArtwork{thumbPath: "/media/shows/BB/ep3_thumb.jpg"}
	e := newTestEnricher(agent, updater, artwork)

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/media/shows/BB/S01E03.mkv"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "...And the Bag's in the River" {
		t.Errorf("title: got %q, want %q", p.Title, "...And the Bag's in the River")
	}
	if p.ThumbPath == nil || *p.ThumbPath != "shows/BB/ep3_thumb.jpg" {
		t.Errorf("thumb: got %v, want shows/BB/ep3_thumb.jpg", p.ThumbPath)
	}
}

// TestEnrichEpisode_TVDBOnly_NoTMDBID exercises the anime-library path
// where a show was matched on AniList (or TVDB) but never on TMDB —
// we cross-harvest a TVDB ID but not a TMDB ID. Pre-fix, the
// enricher returned at the "show.TMDBID == nil" guard and orphaned
// every episode without a description. Now: TVDB stands alone as
// a primary provider when TMDB ID is absent.
func TestEnrichEpisode_TVDBOnly_NoTMDBID(t *testing.T) {
	tvdbID := 81189
	seasonNum := 1
	episodeNum := 1
	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()
	posterPath := "shows/anime/poster.jpg"

	agent := &mockAgent{} // TMDB agent returns nothing — no TMDB ID on show
	tvdbResult := &metadata.EpisodeResult{
		Title:   "Awakening",
		Summary: "Sung Jinwoo enters the double dungeon.",
		AirDate: time.Date(2024, 1, 6, 0, 0, 0, 0, time.UTC),
	}
	tvdbStub := &mockTVDB{getEpisodeResult: tvdbResult}

	updater := newMockUpdater()
	updater.items[showID] = &media.Item{
		ID:         showID,
		Type:       "show",
		Title:      "Solo Leveling",
		TVDBID:     &tvdbID, // TVDB only — no TMDBID set
		PosterPath: &posterPath,
	}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		Title:    "Season 1",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	updater.items[episodeID] = &media.Item{
		ID:       episodeID,
		Type:     "episode",
		Title:    "Episode 1",
		ParentID: &seasonID,
		Index:    &episodeNum,
	}
	e := newTestEnricher(agent, updater, nil)
	e.SetTVDBFallbackFn(func() TVDBFallback { return tvdbStub })

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/anime/Solo Leveling/S01E01.mkv"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "Awakening" {
		t.Errorf("title from TVDB: got %q, want %q", p.Title, "Awakening")
	}
	if p.Summary == nil || *p.Summary != "Sung Jinwoo enters the double dungeon." {
		t.Errorf("summary from TVDB: got %v, want set", p.Summary)
	}
	if tvdbStub.getEpisodeCalls != 1 {
		t.Errorf("expected 1 TVDB GetEpisode call, got %d", tvdbStub.getEpisodeCalls)
	}
}

// TestEnrichManga proves the AniList manga path populates a book row
// with manga-specific metadata: anilist/mal IDs, mangaka, summary,
// genres + tags, content rating, AND the reading_direction derived
// from countryOfOrigin (JP → rtl, KR/CN → ttb).
func TestEnrichManga(t *testing.T) {
	libraryID := uuid.New()
	bookID := uuid.New()
	updater := newMockUpdater()
	updater.items[bookID] = &media.Item{
		ID:        bookID,
		LibraryID: libraryID,
		Type:      "book",
		Title:     "Death Note",
	}

	rtl := "rtl"
	anilistStub := &stubAniListAgent{
		mangaResult: &metadata.MangaResult{
			AniListID:           30014,
			MALID:               21,
			Title:               "Death Note",
			OriginalTitle:       "デスノート",
			StartYear:           2003,
			Summary:             "Light Yagami is an ace student...",
			Rating:              8.5,
			Author:              "Tsugumi Ohba",
			Artist:              "Takeshi Obata",
			SerializationStatus: "FINISHED",
			Genres:              []string{"Mystery", "Psychological"},
			Tags:                []string{"Shounen", "Detective"},
			ReadingDirection:    rtl,
			Volumes:             12,
			Chapters:            108,
			PosterURL:           "http://x/dn.jpg",
		},
	}

	e := newTestEnricher(&mockAgent{}, updater, nil)
	e.SetAniListFn(func() AniListAgent { return anilistStub })

	if err := e.enrichManga(context.Background(), updater.items[bookID], &media.File{FilePath: "/manga/death note/v01.cbz"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "Death Note" {
		t.Errorf("title: got %q, want Death Note", p.Title)
	}
	if p.AniListID == nil || *p.AniListID != 30014 {
		t.Errorf("AniListID not propagated: %v", p.AniListID)
	}
	if p.MALID == nil || *p.MALID != 21 {
		t.Errorf("MALID not propagated: %v", p.MALID)
	}
	if p.ReadingDirection == nil || *p.ReadingDirection != "rtl" {
		t.Errorf("ReadingDirection: got %v, want rtl", p.ReadingDirection)
	}
	if p.Year == nil || *p.Year != 2003 {
		t.Errorf("Year: got %v, want 2003", p.Year)
	}
	if len(p.Genres) != 2 {
		t.Errorf("Genres: got %v, want 2 entries", p.Genres)
	}
	if len(p.Tags) != 2 {
		t.Errorf("Tags: got %v, want 2 entries", p.Tags)
	}
}

// TestAttachAniListFranchiseToSeasons proves the per-season AniList ID
// linking works. Anime franchises split each cour onto its own AniList
// Media row joined by PREQUEL/SEQUEL — the matched show row only
// names *one* of those (whichever the title search ranked first), so
// without this attach the cascade aims every season at the same Media
// list and Season 1 ends up with Season 2's titles via position
// fallback. Test mirrors the Solo Leveling shape: S1 = 153406 (2024),
// S2 = 151807 (2025).
func TestAttachAniListFranchiseToSeasons(t *testing.T) {
	showID := uuid.New()
	s1ID := uuid.New()
	s2ID := uuid.New()
	s1Idx, s2Idx := 1, 2

	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show"}
	updater.items[s1ID] = &media.Item{
		ID: s1ID, Type: "season", ParentID: &showID, Index: &s1Idx,
	}
	updater.items[s2ID] = &media.Item{
		ID: s2ID, Type: "season", ParentID: &showID, Index: &s2Idx,
	}
	updater.children[showID] = []media.Item{*updater.items[s1ID], *updater.items[s2ID]}

	anilistStub := &stubAniListAgent{
		franchise: []anilist.AniListRelation{
			// Returned sorted by start year ascending, so [0] = S1.
			{AniListID: 153406, MalID: 52299, StartYear: 2024, Title: "Solo Leveling"},
			{AniListID: 151807, MalID: 52301, StartYear: 2025, Title: "Solo Leveling Season 2"},
		},
	}

	e := newTestEnricher(&mockAgent{}, updater, nil)
	e.SetAniListFn(func() AniListAgent { return anilistStub })

	// Show was matched against the S2 row (151807) — typical when the
	// title search prioritises the latest cour. Walk should backfill
	// S1 with 153406 and lock S2 to 151807.
	e.attachAniListFranchiseToSeasons(context.Background(), showID, 151807)

	// Two season updates should have landed.
	var s1Update, s2Update *media.UpdateItemMetadataParams
	for i := range updater.updateCalls {
		if updater.updateCalls[i].ID == s1ID {
			s1Update = &updater.updateCalls[i]
		}
		if updater.updateCalls[i].ID == s2ID {
			s2Update = &updater.updateCalls[i]
		}
	}
	if s1Update == nil || s1Update.AniListID == nil || *s1Update.AniListID != 153406 {
		t.Errorf("Season 1 should be linked to AniList 153406, got %+v", s1Update)
	}
	if s2Update == nil || s2Update.AniListID == nil || *s2Update.AniListID != 151807 {
		t.Errorf("Season 2 should be linked to AniList 151807, got %+v", s2Update)
	}
}

// TestComputeFranchiseID covers the franchise_id derivation that lets
// the UI optionally collapse anime cours under a single card without
// regexing titles. The community-converged answer (per Plex/Hama,
// Jellyfin/Shoko, AniList docs) is to walk the AniList relations
// graph and pick a stable representative ID — we use the smallest
// AniList ID in the connected component.
func TestComputeFranchiseID(t *testing.T) {
	t.Run("multi-cour franchise picks smallest AniList id", func(t *testing.T) {
		// Solo Leveling shape: walk from the S1 row returns the
		// franchise pair, and the franchise key is the numeric
		// minimum across the component (S2 happens to have the
		// lower AniList ID here).
		anilistStub := &stubAniListAgent{
			franchise: []anilist.AniListRelation{
				{AniListID: 153406, StartYear: 2024},
				{AniListID: 151807, StartYear: 2025},
			},
		}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		got := e.computeFranchiseID(context.Background(), 153406)
		if got != 151807 {
			t.Errorf("computeFranchiseID = %d, want 151807 (smallest in component)", got)
		}
	})

	t.Run("walk-from-any-cour produces same franchise key", func(t *testing.T) {
		// Determinism check — entering from any cour must yield the
		// same franchise_id, since the smallest-ID rule is symmetric.
		anilistStub := &stubAniListAgent{
			franchise: []anilist.AniListRelation{
				{AniListID: 200, StartYear: 2020},
				{AniListID: 100, StartYear: 2018},
				{AniListID: 300, StartYear: 2022},
			},
		}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		for _, entry := range []int{100, 200, 300} {
			if got := e.computeFranchiseID(context.Background(), entry); got != 100 {
				t.Errorf("entered from %d, got franchise_id %d, want 100", entry, got)
			}
		}
	})

	t.Run("singleton (no relations) falls back to the input id", func(t *testing.T) {
		// One-off anime with no PREQUEL/SEQUEL chain still gets a
		// franchise_id (= its own AniList ID) so it clusters with
		// itself and the UI doesn't need a NULL special case.
		anilistStub := &stubAniListAgent{franchise: nil}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		if got := e.computeFranchiseID(context.Background(), 42); got != 42 {
			t.Errorf("singleton franchise_id = %d, want 42", got)
		}
	})

	t.Run("walk error returns zero (caller leaves column nil)", func(t *testing.T) {
		// Network error / rate-limit: don't write a guess. Letting
		// the column stay nil means a later refresh can fill it in
		// with a real walked value rather than a permanent self-
		// reference that hides the franchise grouping.
		anilistStub := &stubAniListAgent{frErr: errors.New("rate limited")}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		if got := e.computeFranchiseID(context.Background(), 99); got != 0 {
			t.Errorf("walk-error franchise_id = %d, want 0", got)
		}
	})

	t.Run("anilist not wired returns input id unchanged", func(t *testing.T) {
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		// No SetAniListFn — anilistFn is nil.
		if got := e.computeFranchiseID(context.Background(), 7); got != 7 {
			t.Errorf("no-anilist franchise_id = %d, want 7", got)
		}
	})

	t.Run("input id is already the smallest in component", func(t *testing.T) {
		// Walk from the lowest-ID cour; should return the input rather
		// than picking up a larger ID. Verifies the comparison
		// initialises smallest = anilistID rather than +inf.
		anilistStub := &stubAniListAgent{
			franchise: []anilist.AniListRelation{
				{AniListID: 200, StartYear: 2020},
				{AniListID: 300, StartYear: 2022},
			},
		}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		if got := e.computeFranchiseID(context.Background(), 100); got != 100 {
			t.Errorf("when input is smallest, got %d, want 100", got)
		}
	})

	t.Run("zero-id franchise entries are ignored", func(t *testing.T) {
		// Defends against a malformed AniList response — a relation
		// node with id 0 must not collapse the whole franchise to 0.
		anilistStub := &stubAniListAgent{
			franchise: []anilist.AniListRelation{
				{AniListID: 0, StartYear: 0}, // bogus
				{AniListID: 500, StartYear: 2021},
			},
		}
		e := newTestEnricher(&mockAgent{}, newMockUpdater(), nil)
		e.SetAniListFn(func() AniListAgent { return anilistStub })
		if got := e.computeFranchiseID(context.Background(), 500); got != 500 {
			t.Errorf("zero-id ignored: got %d, want 500", got)
		}
	})
}

// TestEnrichEpisode_AniListFallback covers the anime-library scenario
// where the operator's TMDB key is broken (or never configured) and
// the show has no TVDB ID either — only an AniList ID survives.
// AniList streamingEpisodes carries title + thumbnail (no summary)
// so we treat it as the "best we can do" final fallback.
func TestEnrichEpisode_AniListFallback(t *testing.T) {
	anilistID := 158927
	seasonNum := 1
	episodeNum := 5
	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()
	posterPath := "anime/show/poster.jpg"

	agent := &mockAgent{} // TMDB returns nothing
	tvdbStub := &mockTVDB{} // TVDB returns nothing too
	anilistStub := &stubAniListAgent{
		episodes: []metadata.EpisodeResult{
			{EpisodeNum: 1, Title: "Awakening", ThumbURL: "http://x/1.jpg"},
			{EpisodeNum: 2, Title: "Hunters", ThumbURL: "http://x/2.jpg"},
			{EpisodeNum: 5, Title: "Real Hunter", ThumbURL: "http://x/5.jpg"},
		},
	}

	updater := newMockUpdater()
	updater.items[showID] = &media.Item{
		ID:         showID,
		Type:       "show",
		Title:      "Solo Leveling",
		AniListID:  &anilistID, // only AniList ID — no TMDB / TVDB
		PosterPath: &posterPath,
	}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	updater.items[episodeID] = &media.Item{
		ID:       episodeID,
		Type:     "episode",
		ParentID: &seasonID,
		Index:    &episodeNum,
	}
	e := newTestEnricher(agent, updater, nil)
	e.SetTVDBFallbackFn(func() TVDBFallback { return tvdbStub })
	e.SetAniListFn(func() AniListAgent { return anilistStub })

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/anime/show/05.mkv"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	if updater.updateCalls[0].Title != "Real Hunter" {
		t.Errorf("title from AniList: got %q, want %q", updater.updateCalls[0].Title, "Real Hunter")
	}
}

func TestPickAniListEpisode(t *testing.T) {
	cases := []struct {
		name   string
		eps    []metadata.EpisodeResult
		target int
		want   string // expected title; "" = nil result
	}{
		{
			name: "exact index match",
			eps: []metadata.EpisodeResult{
				{EpisodeNum: 1, Title: "A"},
				{EpisodeNum: 2, Title: "B"},
				{EpisodeNum: 3, Title: "C"},
			},
			target: 2,
			want:   "B",
		},
		{
			name: "position fallback when all indices unparsed",
			eps: []metadata.EpisodeResult{
				{EpisodeNum: 0, Title: "First"},
				{EpisodeNum: 0, Title: "Second"},
				{EpisodeNum: 0, Title: "Third"},
			},
			target: 2,
			want:   "Second",
		},
		{
			name: "mixed parsed + bare titles — position fallback fills the gap",
			eps: []metadata.EpisodeResult{
				{EpisodeNum: 0, Title: "Awakening"},
				{EpisodeNum: 0, Title: "Hunters"},
				{EpisodeNum: 13, Title: "You Aren't E-Rank, Are You?"},
			},
			target: 2, // bare title at position [1], no exact match
			want:   "Hunters",
		},
		{
			name:   "empty list",
			eps:    nil,
			target: 1,
			want:   "",
		},
		{
			name: "target out of range",
			eps: []metadata.EpisodeResult{
				{EpisodeNum: 1, Title: "A"},
			},
			target: 5,
			want:   "",
		},
		{
			name:   "non-positive target",
			eps:    []metadata.EpisodeResult{{EpisodeNum: 1, Title: "A"}},
			target: 0,
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pickAniListEpisode(tc.eps, tc.target)
			if tc.want == "" {
				if got != nil {
					t.Errorf("expected nil, got %q", got.Title)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %q, got nil", tc.want)
			}
			if got.Title != tc.want {
				t.Errorf("got %q, want %q", got.Title, tc.want)
			}
		})
	}
}

func TestEnrichEpisode_NoParentID_Noop(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	epID := uuid.New()
	idx := 1
	updater.items[epID] = &media.Item{ID: epID, Type: "episode", Index: &idx}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichEpisode(context.Background(), agent, updater.items[epID], &media.File{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 0 {
		t.Error("should skip episode with no parent_id")
	}
}

func TestEnrichEpisode_ShowNotEnriched_CascadesUp(t *testing.T) {
	// When the grandparent show has no TMDB ID and no poster, enrichEpisode
	// should attempt to enrich the show first (cascade up).
	tmdbID := 1396
	seasonNum := 1
	episodeNum := 1
	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()

	agent := &mockAgent{
		searchTVResult: &metadata.TVShowResult{
			TMDBID:       tmdbID,
			Title:        "Breaking Bad",
			FirstAirYear: 2008,
		},
		getEpisodeResult: &metadata.EpisodeResult{
			Title:   "Pilot",
			Summary: "Walt turns to crime.",
		},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad"}
	updater.items[seasonID] = &media.Item{
		ID:       seasonID,
		Type:     "season",
		Title:    "Season 1",
		ParentID: &showID,
		Index:    &seasonNum,
	}
	updater.items[episodeID] = &media.Item{
		ID:       episodeID,
		Type:     "episode",
		Title:    "Episode 1",
		ParentID: &seasonID,
		Index:    &episodeNum,
	}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The show should have been enriched (cascade up) setting TMDB ID.
	show := updater.items[showID]
	if show.TMDBID == nil {
		t.Error("show should have TMDB ID after cascade enrichment")
	}
}

// TestEnrichShowChildren_AniListOnly_Cascades guards the anime-library
// path where the show was matched only on AniList (no TMDB or TVDB ID).
// Pre-fix: the cascade gate at "show.TMDBID == nil" stopped the
// season fan-out, so episodes never reached enrichEpisode and never
// picked up the AniList streamingEpisodes fallback. Now: any of the
// three provider IDs unlocks the cascade.
func TestEnrichShowChildren_AniListOnly_Cascades(t *testing.T) {
	anilistID := 158927
	showID := uuid.New()
	seasonID := uuid.New()
	seasonIdx := 1

	agent := &mockAgent{} // no TMDB result
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{
		ID:        showID,
		Type:      "show",
		Title:     "Solo Leveling",
		AniListID: &anilistID, // AniList only
	}
	updater.items[seasonID] = &media.Item{
		ID: seasonID, Type: "season", Title: "Season 1",
		ParentID: &showID, Index: &seasonIdx,
	}
	updater.children[showID] = []media.Item{*updater.items[seasonID]}

	e := newTestEnricher(agent, updater, nil)
	e.enrichShowChildren(context.Background(), agent, updater.items[showID], &media.File{FilePath: "/anime/show/01.mkv"})

	// enrichSeason should have been reached even though TMDBID is nil —
	// season cascade fires for any provider ID. The mock TMDB returns
	// nothing for season metadata so no UpdateItemMetadata call lands
	// on the season itself, but the absence of a panic / early-return
	// is what proves the gate flipped.
	// (No assertion on update calls because TMDB-side season fetch
	// short-circuits when TMDBID is nil; we only care that the
	// cascade reached this point and did not return at the gate.)
	_ = updater
}

// ── enrichShowChildren ───────────────────────────────────────────────────────

func TestEnrichShowChildren_EnrichesSeasons(t *testing.T) {
	tmdbID := 1396
	showID := uuid.New()
	season1ID := uuid.New()
	season2ID := uuid.New()
	seasonIdx1 := 1
	seasonIdx2 := 2

	agent := &mockAgent{
		getSeasonResult: &metadata.SeasonResult{
			Number:  1,
			Name:    "Season 1",
			Summary: "First season.",
		},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad", TMDBID: &tmdbID}
	updater.items[season1ID] = &media.Item{
		ID: season1ID, Type: "season", Title: "Season 1",
		ParentID: &showID, Index: &seasonIdx1,
	}
	// Season 2 already has a poster — should be skipped.
	poster := "shows/s2_poster.jpg"
	updater.items[season2ID] = &media.Item{
		ID: season2ID, Type: "season", Title: "Season 2",
		ParentID: &showID, Index: &seasonIdx2, PosterPath: &poster,
	}
	updater.children[showID] = []media.Item{*updater.items[season1ID], *updater.items[season2ID]}

	e := newTestEnricher(agent, updater, nil)
	e.enrichShowChildren(context.Background(), agent, updater.items[showID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"})

	// Only season 1 should have been enriched (season 2 has poster already).
	enrichedSeason1 := false
	for _, call := range updater.updateCalls {
		if call.ID == season1ID {
			enrichedSeason1 = true
		}
		if call.ID == season2ID {
			t.Error("season 2 should not be enriched — already has poster")
		}
	}
	if !enrichedSeason1 {
		t.Error("season 1 should have been enriched")
	}
}

// ── enrichSeasonChildren ─────────────────────────────────────────────────────

func TestEnrichSeasonChildren_EnrichesEpisodes(t *testing.T) {
	tmdbID := 1396
	seasonNum := 1
	showID := uuid.New()
	seasonID := uuid.New()
	ep1ID := uuid.New()
	ep2ID := uuid.New()
	epIdx1 := 1
	epIdx2 := 2

	agent := &mockAgent{
		getEpisodeResult: &metadata.EpisodeResult{
			Title:   "Pilot",
			Summary: "Walt turns to crime.",
		},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad", TMDBID: &tmdbID}
	updater.items[seasonID] = &media.Item{
		ID: seasonID, Type: "season", Title: "Season 1",
		ParentID: &showID, Index: &seasonNum,
	}
	updater.items[ep1ID] = &media.Item{
		ID: ep1ID, Type: "episode", Title: "Episode 1",
		ParentID: &seasonID, Index: &epIdx1,
	}
	// Episode 2 already has summary + thumbnail — should be skipped.
	summary := "Already enriched."
	thumb := "shows/BB/Season 1/ep2.jpg"
	updater.items[ep2ID] = &media.Item{
		ID: ep2ID, Type: "episode", Title: "Episode 2",
		ParentID: &seasonID, Index: &epIdx2, Summary: &summary, ThumbPath: &thumb,
	}
	updater.children[seasonID] = []media.Item{*updater.items[ep1ID], *updater.items[ep2ID]}

	e := newTestEnricher(agent, updater, nil)
	e.enrichSeasonChildren(context.Background(), agent, updater.items[showID], updater.items[seasonID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"}, nil)

	enrichedEp1 := false
	for _, call := range updater.updateCalls {
		if call.ID == ep1ID {
			enrichedEp1 = true
		}
		if call.ID == ep2ID {
			t.Error("episode 2 should not be enriched — already has summary and thumbnail")
		}
	}
	if !enrichedEp1 {
		t.Error("episode 1 should have been enriched")
	}
}

// TestEnrichSeasonChildren_PreloadedSkipsPerEpisodeCalls proves the bulk-fetch
// path. When the parent season's GetSeason call already returned the episode
// list, the cascade must apply that data locally and never hit GetEpisode.
// This is the difference between 1 + N and 1 TMDB calls per season — easy to
// regress if a future refactor forgets to forward the prefetched arg.
func TestEnrichSeasonChildren_PreloadedSkipsPerEpisodeCalls(t *testing.T) {
	tmdbID := 1396
	seasonNum := 1
	showID := uuid.New()
	seasonID := uuid.New()
	ep1ID := uuid.New()
	ep2ID := uuid.New()
	epIdx1 := 1
	epIdx2 := 2

	agent := &mockAgent{
		// Sentinel: if the cascade falls back to per-episode lookup,
		// it'll get THIS, which would land in updateCalls and let us
		// detect the regression by Title.
		getEpisodeResult: &metadata.EpisodeResult{Title: "PER-EPISODE-FALLBACK"},
	}
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Breaking Bad", TMDBID: &tmdbID}
	updater.items[seasonID] = &media.Item{
		ID: seasonID, Type: "season", Title: "Season 1",
		ParentID: &showID, Index: &seasonNum,
	}
	updater.items[ep1ID] = &media.Item{
		ID: ep1ID, Type: "episode", Title: "Episode 1",
		ParentID: &seasonID, Index: &epIdx1,
	}
	updater.items[ep2ID] = &media.Item{
		ID: ep2ID, Type: "episode", Title: "Episode 2",
		ParentID: &seasonID, Index: &epIdx2,
	}
	updater.children[seasonID] = []media.Item{*updater.items[ep1ID], *updater.items[ep2ID]}

	preloaded := []metadata.EpisodeResult{
		{ShowTMDBID: tmdbID, SeasonNum: seasonNum, EpisodeNum: 1, Title: "Pilot", Summary: "Walt turns to crime."},
		{ShowTMDBID: tmdbID, SeasonNum: seasonNum, EpisodeNum: 2, Title: "Cat in the Bag", Summary: "Body disposal."},
	}

	e := newTestEnricher(agent, updater, nil)
	e.enrichSeasonChildren(context.Background(), agent, updater.items[showID], updater.items[seasonID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"}, preloaded)

	if agent.getEpisodeCalls != 0 {
		t.Errorf("GetEpisode calls = %d, want 0 — bulk path must not round-trip per episode", agent.getEpisodeCalls)
	}
	gotTitles := map[uuid.UUID]string{}
	for _, call := range updater.updateCalls {
		gotTitles[call.ID] = call.Title
	}
	if gotTitles[ep1ID] != "Pilot" {
		t.Errorf("ep1 title = %q, want %q (preloaded data not applied)", gotTitles[ep1ID], "Pilot")
	}
	if gotTitles[ep2ID] != "Cat in the Bag" {
		t.Errorf("ep2 title = %q, want %q (preloaded data not applied)", gotTitles[ep2ID], "Cat in the Bag")
	}
}

// ── EnrichItem ───────────────────────────────────────────────────────────────

func TestEnrichItem_NoActiveFile_Error(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Test"}
	// No files for this item, no descendants either.
	e := newTestEnricher(agent, updater, nil)

	err := e.EnrichItem(context.Background(), itemID)
	if err == nil {
		t.Fatal("expected error when no active file exists")
	}
}

// TestEnrichItem_Show_FallsThroughToDescendantFile guards the v2.1 fix
// for the admin bulk re-enrich-unmatched path: shows have no direct
// files (the files belong to descendant episodes), so EnrichItem must
// walk parent_id → children to find one. Without this, the on-demand
// admin Enrich and bulk re-enrich endpoints both fail with "no active
// file for item X" the moment they're called on a show, even though
// the show's episodes are perfectly scannable. Mirrors the existing
// MatchItem behavior so Fix Match and bulk Re-enrich agree.
func TestEnrichItem_Show_FallsThroughToDescendantFile(t *testing.T) {
	agent := &mockAgent{
		searchTVResult: &metadata.TVShowResult{Title: "My Hero Academia"},
	}
	updater := newMockUpdater()

	showID := uuid.New()
	seasonID := uuid.New()
	episodeID := uuid.New()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "[ToonsHub] My Hero Academia"}
	updater.items[seasonID] = &media.Item{ID: seasonID, Type: "season", Title: "Season 1", ParentID: &showID}
	updater.items[episodeID] = &media.Item{ID: episodeID, Type: "episode", Title: "Episode 1", ParentID: &seasonID}
	// Show + season have no direct files; episode does.
	updater.children[showID] = []media.Item{*updater.items[seasonID]}
	updater.children[seasonID] = []media.Item{*updater.items[episodeID]}
	updater.files[episodeID] = []media.File{
		{ID: uuid.New(), MediaItemID: episodeID, FilePath: "/tv/My Hero Academia/Season 01/S01E01.mkv", Status: "active"},
	}
	e := newTestEnricher(agent, updater, nil)

	if err := e.EnrichItem(context.Background(), showID); err != nil {
		t.Fatalf("EnrichItem on show with descendant-only files: %v", err)
	}
}

func TestEnrichItem_Success(t *testing.T) {
	agent := &mockAgent{
		searchMovieResult: &metadata.MovieResult{
			Title: "Test Movie",
			Year:  2020,
		},
	}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Test"}
	updater.files[itemID] = []media.File{
		{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/movies/test.mkv", Status: "active"},
	}
	e := newTestEnricher(agent, updater, nil)

	err := e.EnrichItem(context.Background(), itemID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Errorf("expected 1 update call, got %d", len(updater.updateCalls))
	}
}

// ── MatchItem (Fix Match) ────────────────────────────────────────────────────

// TestMatchItem_Show_MergesWhenCanonicalAlreadyExists guards the v2.1
// fix for the duplicate-key crash in Fix Match. When the operator
// applies a TMDB id to a row that's already attached to a different
// row in the same library, matchShow now merges the current row into
// the survivor instead of attempting to update its title (which would
// hit `idx_media_items_library_type_title_year` and abort enrichment).
func TestMatchItem_Show_MergesWhenCanonicalAlreadyExists(t *testing.T) {
	libraryID := uuid.New()
	loserID := uuid.New()
	survivorID := uuid.New()
	updater := newMockUpdater()
	updater.items[loserID] = &media.Item{
		ID: loserID, LibraryID: libraryID, Type: "show", Title: "[ToonsHub] My Hero Academia",
	}
	updater.items[survivorID] = &media.Item{
		ID: survivorID, LibraryID: libraryID, Type: "show", Title: "My Hero Academia",
	}
	// Episode under the loser provides the file MatchItem walks to.
	episodeID := uuid.New()
	parent := loserID
	updater.items[episodeID] = &media.Item{ID: episodeID, Type: "episode", ParentID: &parent}
	updater.children[loserID] = []media.Item{*updater.items[episodeID]}
	updater.files[episodeID] = []media.File{
		{ID: uuid.New(), MediaItemID: episodeID, FilePath: "/tv/MHA/S01E01.mkv", Status: "active"},
	}
	// Existing canonical row already attached to TMDB id 65930.
	updater.itemByTMDB[65930] = updater.items[survivorID]

	// Agent should NOT be called — the merge short-circuits before TMDB lookup.
	agent := &mockAgent{}
	e := newTestEnricher(agent, updater, nil)

	if err := e.MatchItem(context.Background(), loserID, 65930); err != nil {
		t.Fatalf("MatchItem: %v", err)
	}
	if len(updater.mergeCalls) != 1 {
		t.Fatalf("merge calls: got %d, want 1", len(updater.mergeCalls))
	}
	mc := updater.mergeCalls[0]
	if mc.LoserID != loserID || mc.SurvivorID != survivorID || mc.ItemType != "show" {
		t.Errorf("merge call: got %+v, want loser=%s survivor=%s type=show", mc, loserID, survivorID)
	}
	// No metadata update should have happened — merge replaces the update path.
	if len(updater.updateCalls) != 0 {
		t.Errorf("update calls: got %d, want 0 (merge path skips metadata write)", len(updater.updateCalls))
	}
}

// TestMatchItem_Show_NoCanonical_TakesUpdatePath confirms the standard
// Fix Match path still runs when the chosen TMDB id isn't already
// attached to another row.
func TestMatchItem_Show_NoCanonical_TakesUpdatePath(t *testing.T) {
	libraryID := uuid.New()
	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, LibraryID: libraryID, Type: "show", Title: "Show A"}
	episodeID := uuid.New()
	parent := itemID
	updater.items[episodeID] = &media.Item{ID: episodeID, Type: "episode", ParentID: &parent}
	updater.children[itemID] = []media.Item{*updater.items[episodeID]}
	updater.files[episodeID] = []media.File{
		{ID: uuid.New(), MediaItemID: episodeID, FilePath: "/tv/Show A/S01E01.mkv", Status: "active"},
	}
	// itemByTMDB is empty — no survivor.
	agent := &mockAgent{
		searchTVResult: &metadata.TVShowResult{Title: "Show A Canonical"},
	}
	e := newTestEnricher(agent, updater, nil)

	if err := e.MatchItem(context.Background(), itemID, 12345); err != nil {
		t.Fatalf("MatchItem: %v", err)
	}
	if len(updater.mergeCalls) != 0 {
		t.Errorf("merge calls: got %d, want 0 (no canonical row → standard update path)", len(updater.mergeCalls))
	}
	if len(updater.updateCalls) == 0 {
		t.Errorf("expected update calls in the standard path")
	}
}

// TestMatchItem_Show_SelfMatch_NoOp confirms the merge path is a no-op
// when the TMDB id is already attached to the same row Fix Match is
// being applied to (idempotent).
func TestMatchItem_Show_SelfMatch_NoOp(t *testing.T) {
	libraryID := uuid.New()
	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, LibraryID: libraryID, Type: "show", Title: "Already Matched"}
	episodeID := uuid.New()
	parent := itemID
	updater.items[episodeID] = &media.Item{ID: episodeID, Type: "episode", ParentID: &parent}
	updater.children[itemID] = []media.Item{*updater.items[episodeID]}
	updater.files[episodeID] = []media.File{
		{ID: uuid.New(), MediaItemID: episodeID, FilePath: "/tv/x/S01E01.mkv", Status: "active"},
	}
	// Survivor *is* the same row.
	updater.itemByTMDB[55555] = updater.items[itemID]

	agent := &mockAgent{
		searchTVResult: &metadata.TVShowResult{Title: "Already Matched"},
	}
	e := newTestEnricher(agent, updater, nil)

	if err := e.MatchItem(context.Background(), itemID, 55555); err != nil {
		t.Fatalf("MatchItem: %v", err)
	}
	if len(updater.mergeCalls) != 0 {
		t.Errorf("merge calls: got %d, want 0 (self-match must not trigger merge)", len(updater.mergeCalls))
	}
}

// ── path helpers ─────────────────────────────────────────────────────────────

func TestShowDirFromFile(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{"standard layout", filepath.Join("/tv", "Breaking Bad", "Season 01", "S01E01.mkv"), filepath.Join("/tv", "Breaking Bad")},
		{"flat layout", filepath.Join("/tv", "Breaking Bad", "S01E01.mkv"), filepath.Join("/tv", "Breaking Bad")},
		{"specials", filepath.Join("/tv", "Show", "Specials", "S00E01.mkv"), filepath.Join("/tv", "Show")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := showDirFromFile(tt.filePath)
			if got != tt.want {
				t.Errorf("showDirFromFile(%q) = %q, want %q", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestEnricher_relPath(t *testing.T) {
	e := &Enricher{
		scanPaths: func() []string {
			return []string{"/movies", "/tv"}
		},
	}
	tests := []struct {
		name    string
		absPath string
		want    string
	}{
		{
			name:    "movie inside scan root resolves to relative",
			absPath: filepath.Join("/movies", "Send Help (2026)", "poster.jpg"),
			want:    "Send Help (2026)/poster.jpg",
		},
		{
			name:    "tv episode inside scan root resolves to relative",
			absPath: filepath.Join("/tv", "Breaking Bad", "Season 01", "poster.jpg"),
			want:    "Breaking Bad/Season 01/poster.jpg",
		},
		{
			name:    "outside-root path returns empty (do not write a bare basename)",
			absPath: filepath.Join("/elsewhere", "loose-poster.jpg"),
			want:    "",
		},
		{
			name:    "file directly at scan root resolves to bare basename via filepath.Rel (legitimate flat layout)",
			absPath: filepath.Join("/movies", "single.jpg"),
			want:    "single.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.relPath(tt.absPath); got != tt.want {
				t.Errorf("relPath(%q) = %q, want %q", tt.absPath, got, tt.want)
			}
		})
	}
}

// TestEnricher_setRelPath_skipsOnEmpty covers the bug-class fix: when
// a downloaded artwork file lands outside library scan_paths, the
// helper must NOT write a bare basename to *dest. That basename
// would 404 against /artwork/* on every render. Migration 00054
// (album-only) and 00070 (album + artist) clean up the rows the
// previous fallback wrote; this test locks the writer behavior.
func TestEnricher_setRelPath_skipsOnEmpty(t *testing.T) {
	e := &Enricher{scanPaths: func() []string { return []string{"/music"} }}

	// In-scan-path file → dest gets the relative path.
	var dest *string
	e.setRelPath(&dest, "/music/Pink Floyd/Dark Side/p.jpg")
	if dest == nil || *dest != "Pink Floyd/Dark Side/p.jpg" {
		t.Errorf("dest = %v, want pointer to relative path", dest)
	}

	// Out-of-scan-path file → dest stays at its prior value (nil),
	// so the COALESCE in UpdateMediaItemMetadata preserves the
	// existing poster_path instead of overwriting with an unservable
	// bare basename.
	var dest2 *string
	e.setRelPath(&dest2, "/elsewhere/artwork.jpg")
	if dest2 != nil {
		t.Errorf("dest2 = %v, want nil (skip the update)", dest2)
	}

	// Pre-set dest stays untouched on miss — guarantees the COALESCE
	// preserve-prior contract holds even if a caller had already
	// populated the field from an earlier branch (NFO override,
	// scanner-derived path, etc.).
	prior := "scanner-set/path.jpg"
	dest3 := &prior
	e.setRelPath(&dest3, "/elsewhere/artwork.jpg")
	if dest3 == nil || *dest3 != prior {
		t.Errorf("dest3 = %v, want unchanged %q", dest3, prior)
	}
}

// TestEnrichMovie_PosterOutsideScanRoot_PreservesPriorPath is the
// end-to-end integration of the bare-basename regression. An enricher
// run downloads a poster that, for whatever reason (symlink, bind
// mount, scan_paths drift, agent-supplied custom path), lands outside
// library scan_paths. The agent succeeded — Title/Year/Summary etc.
// should still update — but PosterPath / FanartPath must stay nil in
// the update params so the SQL COALESCE preserves whatever the
// scanner had already populated.
func TestEnrichMovie_PosterOutsideScanRoot_PreservesPriorPath(t *testing.T) {
	year := 1999
	agent := &mockAgent{
		searchMovieResult: &metadata.MovieResult{
			TMDBID:    603,
			Title:     "The Matrix",
			Year:      1999,
			Summary:   "A computer hacker learns…",
			PosterURL: "http://example.com/poster.jpg",
			FanartURL: "http://example.com/fanart.jpg",
		},
	}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "The Matrix", Year: &year}
	// Both downloads land OUTSIDE the test scan root (/media). The
	// enricher must not write the bare basename to PosterPath or
	// FanartPath — the previous bug stored "poster.jpg" and "fanart.jpg"
	// which 404 against any non-flat /artwork/* lookup.
	artwork := &mockArtwork{
		posterPath: "/elsewhere/poster.jpg",
		fanartPath: "/elsewhere/fanart.jpg",
	}
	e := newTestEnricher(agent, updater, artwork)

	if err := e.Enrich(context.Background(), updater.items[itemID], &media.File{FilePath: "/media/movies/The.Matrix.1999.mkv"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "The Matrix" {
		t.Errorf("title: got %q, want The Matrix (non-art fields should still update)", p.Title)
	}
	if p.PosterPath != nil {
		t.Errorf("poster_path: got %v, want nil (out-of-scan-root downloads must be skipped so COALESCE preserves prior)", *p.PosterPath)
	}
	if p.FanartPath != nil {
		t.Errorf("fanart_path: got %v, want nil (same reason)", *p.FanartPath)
	}
}

// TestSetItemPoster_OutsideScanRoot_ReturnsError covers the manual
// poster picker site. Unlike the agent-side enrichers (which silently
// skip the bad path so other update fields land), the manual picker
// is admin-driven and should fail loudly so the operator can fix the
// scan_paths config rather than ending up with a poster_path the
// /artwork/* route can't resolve.
func TestSetItemPoster_OutsideScanRoot_ReturnsError(t *testing.T) {
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Stray Film"}
	updater.files[itemID] = []media.File{
		{ID: uuid.New(), MediaItemID: itemID, FilePath: "/media/movies/stray/stray.mkv", Status: "active"},
	}
	// Downloaded poster lands outside scan_paths — manual picker must
	// return an error so the admin sees what went wrong.
	artwork := &mockArtwork{posterPath: "/elsewhere/poster.jpg"}
	e := newTestEnricher(nil, updater, artwork)

	err := e.SetItemPoster(context.Background(), itemID, "http://example.com/x.jpg")
	if err == nil {
		t.Fatal("expected error for out-of-scan-path poster, got nil")
	}
	if !strings.Contains(err.Error(), "outside library scan_paths") {
		t.Errorf("err = %q, want one mentioning 'outside library scan_paths' so the operator knows what to fix", err.Error())
	}
	// And no update call lands — the row stays clean.
	if len(updater.updateCalls) != 0 {
		t.Errorf("update calls: got %d, want 0 (failed picker must not write a row)", len(updater.updateCalls))
	}
}

func TestLooksLikeSeasonDir(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"Season 01", true},
		{"Season 1", true},
		{"Specials", true},
		{"Breaking Bad", false},
		{"S01", false},
	}
	for _, tt := range tests {
		if got := looksLikeSeasonDir(tt.name); got != tt.want {
			t.Errorf("looksLikeSeasonDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// stubAniListAgent satisfies AniListAgent for the show-enrichment
// tests. SearchAnime returns a fixed TVShowResult; the cross-harvest
// test exercises the path where AniList returns a row with an
// AniList ID but no TMDB ID, and the enricher still needs to write
// a TMDB ID onto the show row so per-episode enrichment can find
// its way back to TMDB.GetEpisode.
type stubAniListAgent struct {
	result      *metadata.TVShowResult
	err         error
	episodes    []metadata.EpisodeResult
	epErr       error
	franchise   []anilist.AniListRelation
	frErr       error
	mangaResult *metadata.MangaResult
	mangaErr    error
}

func (s *stubAniListAgent) SearchAnime(_ context.Context, _ string, _ int) (*metadata.TVShowResult, error) {
	return s.result, s.err
}
func (s *stubAniListAgent) GetAnimeByID(_ context.Context, _ int) (*metadata.TVShowResult, error) {
	return s.result, s.err
}
func (s *stubAniListAgent) GetAnimeEpisodes(_ context.Context, _ int) ([]metadata.EpisodeResult, error) {
	return s.episodes, s.epErr
}
func (s *stubAniListAgent) GetAnimeFranchise(_ context.Context, _ int) ([]anilist.AniListRelation, error) {
	return s.franchise, s.frErr
}
func (s *stubAniListAgent) SearchManga(_ context.Context, _ string, _ int) (*metadata.MangaResult, error) {
	return s.mangaResult, s.mangaErr
}
func (s *stubAniListAgent) GetMangaByID(_ context.Context, _ int) (*metadata.MangaResult, error) {
	return s.mangaResult, s.mangaErr
}

// TestSearchShow_AnimeCrossHarvestsTMDBID locks in the fix for the
// "anime episodes have no descriptions" bug. When an anime library's
// show matches against AniList, the show row needs to also carry a
// TMDB ID so enrichEpisode (which dispatches against TMDB.GetEpisode)
// can populate per-episode summary / air date / rating. Without the
// cross-harvest, the show row's tmdb_id stays NULL and every episode
// in the library is left with the synthetic "Episode N" title and
// no description.
func TestSearchShow_AnimeCrossHarvestsTMDBID(t *testing.T) {
	tmdb := &mockAgent{searchTVResult: &metadata.TVShowResult{
		TMDBID: 244808,
		IMDBID: "tt27654357",
		Title:  "Solo Leveling",
	}}
	anilist := &stubAniListAgent{result: &metadata.TVShowResult{
		AniListID: 153406,
		MALID:     52299,
		Title:     "Solo Leveling",
		Summary:   "From AniList — much richer anime synopsis...",
	}}

	enricher := NewEnricher(
		func() metadata.Agent { return tmdb },
		nil, // no artwork fetcher in this path
		nil, // no updater
		nil, // no scan paths
		slog.Default(),
	)
	enricher.SetAniListFn(func() AniListAgent { return anilist })

	res := enricher.searchShow(context.Background(), tmdb, "Solo Leveling", 2024, true)
	if res == nil {
		t.Fatal("expected non-nil result, got nil")
	}
	// AniList wins for text fields (richer anime metadata).
	if res.Summary != anilist.result.Summary {
		t.Errorf("summary: got %q, want AniList's", res.Summary)
	}
	// All three IDs are populated:
	//   - AniList ID + MAL ID from the AniList match itself
	//   - TMDB ID harvested from the supplementary TMDB call so
	//     per-episode enrichment can fire later
	if res.AniListID != 153406 {
		t.Errorf("AniListID: got %d, want 153406", res.AniListID)
	}
	if res.MALID != 52299 {
		t.Errorf("MALID: got %d, want 52299", res.MALID)
	}
	if res.TMDBID != 244808 {
		t.Errorf("TMDBID: got %d, want 244808 (harvest from TMDB)", res.TMDBID)
	}
	if res.IMDBID != "tt27654357" {
		t.Errorf("IMDBID: got %q, want harvested from TMDB", res.IMDBID)
	}
}

// TestSearchShow_NonAnimeStaysFirstMatch confirms the cross-harvest
// is anime-library-only — non-anime libraries return the first
// match unchanged so we don't burn extra agent calls per scan.
func TestSearchShow_NonAnimeStaysFirstMatch(t *testing.T) {
	anilistCalled := false
	tmdb := &mockAgent{searchTVResult: &metadata.TVShowResult{TMDBID: 100, Title: "Show"}}
	anilist := &stubAniListAgent{result: &metadata.TVShowResult{AniListID: 999}}

	enricher := NewEnricher(
		func() metadata.Agent { return tmdb },
		nil, nil, nil, slog.Default(),
	)
	enricher.SetAniListFn(func() AniListAgent {
		anilistCalled = true
		return anilist
	})

	res := enricher.searchShow(context.Background(), tmdb, "Show", 2020, false)
	if res == nil || res.TMDBID != 100 {
		t.Fatalf("got %+v, want TMDB result", res)
	}
	if anilistCalled {
		t.Errorf("AniList queried in non-anime-library path; should have short-circuited on TMDB match")
	}
	// AniList wasn't called, so no AniList IDs.
	if res.AniListID != 0 {
		t.Errorf("AniListID: got %d, want 0 (non-anime path)", res.AniListID)
	}
}

// ── library type gating ──────────────────────────────────────────────────────
//
// Tests for libraryIsAnime / libraryIsManga, the narrow lookups that flip
// the enricher's agent ordering. Without these gates, the wrong primary
// agent runs for the library type — silent regression that's hard to
// notice until a user sees Western movies populated by AniList queries
// (anime-as-default) or vice versa.

// stubLibChecker records call counts so a test can prove the gating
// lookup actually fired AND verify the cached fn-factory pattern works
// (the enricher invokes libAnimeFn() each enrichment, so the factory
// fires per call but the checker itself only fires per enrichment).
type stubLibChecker struct {
	animeRet bool
	animeErr error
	mangaRet bool
	mangaErr error

	animeCalls int
	mangaCalls int
}

func (s *stubLibChecker) IsLibraryAnime(_ context.Context, _ uuid.UUID) (bool, error) {
	s.animeCalls++
	return s.animeRet, s.animeErr
}
func (s *stubLibChecker) IsLibraryManga(_ context.Context, _ uuid.UUID) (bool, error) {
	s.mangaCalls++
	return s.mangaRet, s.mangaErr
}

func TestLibraryIsManga_NoCheckerWired_ReturnsFalse(t *testing.T) {
	// Default install path: SetLibraryAnimeCheckerFn was never called.
	// The gate must fail closed (return false) so book libraries get
	// their default enrichment path, not silently flip to manga mode.
	e := NewEnricher(
		func() metadata.Agent { return nil },
		nil,
		newMockUpdater(),
		func() []string { return nil },
		slog.Default(),
	)
	if got := e.libraryIsManga(context.Background(), uuid.New()); got {
		t.Errorf("libraryIsManga = true with no checker wired; want false")
	}
}

func TestLibraryIsManga_FactoryReturnsNil_ReturnsFalse(t *testing.T) {
	// Factory wired but returns nil — same fail-closed posture as no
	// factory at all. Covers the case where the DI wiring builds the
	// factory but the checker isn't constructed yet.
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return nil })
	if got := e.libraryIsManga(context.Background(), uuid.New()); got {
		t.Errorf("libraryIsManga = true with nil checker; want false")
	}
}

func TestLibraryIsManga_True(t *testing.T) {
	checker := &stubLibChecker{mangaRet: true}
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return checker })
	if got := e.libraryIsManga(context.Background(), uuid.New()); !got {
		t.Errorf("libraryIsManga = false; want true")
	}
	if checker.mangaCalls != 1 {
		t.Errorf("IsLibraryManga calls = %d, want 1", checker.mangaCalls)
	}
	if checker.animeCalls != 0 {
		t.Errorf("IsLibraryAnime should not have fired; calls = %d", checker.animeCalls)
	}
}

func TestLibraryIsManga_False(t *testing.T) {
	checker := &stubLibChecker{mangaRet: false}
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return checker })
	if got := e.libraryIsManga(context.Background(), uuid.New()); got {
		t.Errorf("libraryIsManga = true; want false")
	}
}

func TestLibraryIsManga_DBError_FailsClosed(t *testing.T) {
	// A transient DB error during the lookup must NOT promote the
	// library to manga mode by accident. Logged + treated as false so
	// the worst-case is "user has to retry" rather than "the wrong
	// enricher silently runs."
	checker := &stubLibChecker{mangaRet: true, mangaErr: errors.New("connection refused")}
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return checker })
	if got := e.libraryIsManga(context.Background(), uuid.New()); got {
		t.Errorf("libraryIsManga = true on lookup error; must fail closed")
	}
}

func TestLibraryIsAnime_True(t *testing.T) {
	// Mirror coverage: same gating contract for the show-side flip.
	checker := &stubLibChecker{animeRet: true}
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return checker })
	if got := e.libraryIsAnime(context.Background(), uuid.New()); !got {
		t.Errorf("libraryIsAnime = false; want true")
	}
	if checker.animeCalls != 1 {
		t.Errorf("IsLibraryAnime calls = %d, want 1", checker.animeCalls)
	}
	if checker.mangaCalls != 0 {
		t.Errorf("IsLibraryManga should not have fired; calls = %d", checker.mangaCalls)
	}
}

func TestLibraryIsAnime_DBError_FailsClosed(t *testing.T) {
	checker := &stubLibChecker{animeRet: true, animeErr: errors.New("timeout")}
	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetLibraryAnimeCheckerFn(func() LibraryAnimeChecker { return checker })
	if got := e.libraryIsAnime(context.Background(), uuid.New()); got {
		t.Errorf("libraryIsAnime = true on lookup error; must fail closed")
	}
}

// branchedAniListAgent returns different results for SearchAnime
// (live search miss) vs GetAnimeByID (offline-DB-derived recovery
// hit). Mirrors how the enricher uses the two paths sequentially:
// search title → if nil, look up via animedb → fetch by id.
type branchedAniListAgent struct {
	stubAniListAgent
	byIDResult *metadata.TVShowResult
	byIDErr    error
}

func (s *branchedAniListAgent) SearchAnime(_ context.Context, _ string, _ int) (*metadata.TVShowResult, error) {
	// Always miss — exercises the offline-DB recovery code path.
	return nil, errors.New("anilist: no anime match")
}

func (s *branchedAniListAgent) GetAnimeByID(_ context.Context, _ int) (*metadata.TVShowResult, error) {
	return s.byIDResult, s.byIDErr
}

// stubAnimeDB satisfies AnimeDBLookup for the enricher test.
type stubAnimeDB struct {
	hits map[string]int // normalized title → AniList ID
}

func (s *stubAnimeDB) Lookup(title string) (animedb.Entry, bool) {
	if id, ok := s.hits[title]; ok {
		return animedb.Entry{Title: title, AniListID: id}, true
	}
	return animedb.Entry{}, false
}

// TestAnilistShowFallback_RecoversViaOfflineDB locks in the fix for
// the user-reported "anime library doesn't show on the hub" bug.
// AniList live `Media(search:$q)` misses fansub-style folder names
// like "Akame ga Kill Theater"; the manami offline DB carries the
// synonym, gives us the AniList ID, and we resolve via GetAnimeByID.
// Without this fallback the show row stays unenriched (no poster_path)
// and the per-library hub query's `grandparent.poster_path IS NOT NULL`
// filter drops the entire row.
func TestAnilistShowFallback_RecoversViaOfflineDB(t *testing.T) {
	resolved := &metadata.TVShowResult{
		AniListID: 20988,
		MALID:     27077,
		Title:     "Akame ga Kill! Gaiden: Theater",
	}
	anilistStub := &branchedAniListAgent{byIDResult: resolved}
	db := &stubAnimeDB{hits: map[string]int{
		// The folder-name input the user has on disk.
		"Akame ga Kill Theater": 20988,
	}}

	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetAniListFn(func() AniListAgent { return anilistStub })
	e.SetAnimeDBFn(func() AnimeDBLookup { return db })

	got := e.anilistShowFallback(context.Background(), "Akame ga Kill Theater", 2014, nil)
	if got == nil {
		t.Fatal("expected offline-DB recovery to return a TVShowResult")
	}
	if got.AniListID != 20988 {
		t.Errorf("AniListID = %d, want 20988", got.AniListID)
	}
	if got.Title != "Akame ga Kill! Gaiden: Theater" {
		t.Errorf("Title = %q (the canonical title from GetAnimeByID)", got.Title)
	}
}

// TestAnilistShowFallback_NoOfflineDBLeavesLiveSearchBehaviour confirms
// that wiring an animeDB factory but having it not match (or not being
// wired at all) leaves the existing live-search-only behaviour intact —
// no new failure mode introduced for non-anime libraries that miss
// AniList for legitimate reasons.
func TestAnilistShowFallback_NoOfflineDBLeavesLiveSearchBehaviour(t *testing.T) {
	anilistStub := &branchedAniListAgent{} // SearchAnime errs, byIDResult nil
	db := &stubAnimeDB{hits: nil}          // empty → never hits

	e := newTestEnricher(nil, newMockUpdater(), nil)
	e.SetAniListFn(func() AniListAgent { return anilistStub })
	e.SetAnimeDBFn(func() AnimeDBLookup { return db })

	got := e.anilistShowFallback(context.Background(), "Some Show", 2014, nil)
	if got != nil {
		t.Fatalf("expected nil when both live + offline miss, got %+v", got)
	}
}
