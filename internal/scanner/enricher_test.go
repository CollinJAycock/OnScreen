package scanner

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
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
	if m.getSeasonErr != nil {
		return nil, m.getSeasonErr
	}
	return m.getSeasonResult, nil
}
func (m *mockAgent) GetEpisode(_ context.Context, _, _, _ int) (*metadata.EpisodeResult, error) {
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

type mockUpdater struct {
	items       map[uuid.UUID]*media.Item
	children    map[uuid.UUID][]media.Item // parentID -> children
	files       map[uuid.UUID][]media.File
	updateCalls []media.UpdateItemMetadataParams
}

func newMockUpdater() *mockUpdater {
	return &mockUpdater{
		items:    make(map[uuid.UUID]*media.Item),
		children: make(map[uuid.UUID][]media.Item),
		files:    make(map[uuid.UUID][]media.File),
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

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/media/shows/BB/S01E03.mkv"})
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

func TestEnrichEpisode_NoParentID_Noop(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	epID := uuid.New()
	idx := 1
	updater.items[epID] = &media.Item{ID: epID, Type: "episode", Index: &idx}
	e := newTestEnricher(agent, updater, nil)

	err := e.enrichEpisode(context.Background(), agent, updater.items[epID], &media.File{})
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

	err := e.enrichEpisode(context.Background(), agent, updater.items[episodeID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The show should have been enriched (cascade up) setting TMDB ID.
	show := updater.items[showID]
	if show.TMDBID == nil {
		t.Error("show should have TMDB ID after cascade enrichment")
	}
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
	e.enrichSeasonChildren(context.Background(), agent, updater.items[showID], updater.items[seasonID], &media.File{FilePath: "/media/shows/BB/S01E01.mkv"})

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

// ── EnrichItem ───────────────────────────────────────────────────────────────

func TestEnrichItem_NoActiveFile_Error(t *testing.T) {
	agent := &mockAgent{}
	updater := newMockUpdater()
	itemID := uuid.New()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Test"}
	// No files for this item.
	e := newTestEnricher(agent, updater, nil)

	err := e.EnrichItem(context.Background(), itemID)
	if err == nil {
		t.Fatal("expected error when no active file exists")
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
		absPath string
		want    string
	}{
		{filepath.Join("/movies", "Send Help (2026)", "poster.jpg"), "Send Help (2026)/poster.jpg"},
		{filepath.Join("/tv", "Breaking Bad", "Season 01", "poster.jpg"), "Breaking Bad/Season 01/poster.jpg"},
	}
	for _, tt := range tests {
		got := e.relPath(tt.absPath)
		if got != tt.want {
			t.Errorf("relPath(%q) = %q, want %q", tt.absPath, got, tt.want)
		}
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
