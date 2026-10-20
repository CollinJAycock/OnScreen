package scanner

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/plugin"
)

type fakeMetadataPlugins struct {
	results []plugin.MetadataResult
	reqs    []plugin.MetadataRequest
	panics  bool
}

func (f *fakeMetadataPlugins) Active(context.Context) bool { return true }

func (f *fakeMetadataPlugins) Enrich(_ context.Context, req plugin.MetadataRequest) []plugin.MetadataResult {
	if f.panics {
		panic("plugin decoder blew up")
	}
	f.reqs = append(f.reqs, req)
	return f.results
}

func sp(s string) *string   { return &s }
func fp(f float64) *float64 { return &f }
func fill(r plugin.MetadataResult) plugin.MetadataResult {
	r.Mode = plugin.MetadataModeFill
	return r
}
func override(r plugin.MetadataResult) plugin.MetadataResult {
	r.Mode = plugin.MetadataModeOverride
	return r
}

func baseItem() *media.Item {
	return &media.Item{
		ID: uuid.New(), Type: "movie",
		Title: "Blade Runner", SortTitle: "Blade Runner",
		Summary:        sp("TMDB summary"),
		Year:           ip(1982),
		AudienceRating: fp(7.5),
		DurationMS:     func() *int64 { v := int64(7000000); return &v }(),
		Tags:           []string{"keep"},
		TMDBID:         ip(78),
	}
}

func TestMergePluginMetadata_FillOnlySetsEmptyFields(t *testing.T) {
	item := baseItem()
	p, changed, _ := mergePluginMetadata(item, []plugin.MetadataResult{fill(plugin.MetadataResult{
		Title:   sp("Plugin Title"),
		Summary: sp("plugin summary"),
		Tagline: sp("More human than human"),
		Year:    ip(1983),
		Genres:  []string{"Sci-Fi"},
	})})
	if p.Title != "Blade Runner" || *p.Summary != "TMDB summary" || *p.Year != 1982 {
		t.Errorf("fill overwrote existing values: title=%q summary=%q year=%d", p.Title, *p.Summary, *p.Year)
	}
	if p.Tagline == nil || *p.Tagline != "More human than human" || !slices.Equal(p.Genres, []string{"Sci-Fi"}) {
		t.Errorf("fill did not set empty fields: tagline=%v genres=%v", p.Tagline, p.Genres)
	}
	if !slices.Equal(changed, []string{media.FieldTagline, media.FieldGenres}) {
		t.Errorf("changed = %v", changed)
	}
	// Columns UpdateItemMetadata writes unconditionally are carried over.
	if p.AudienceRating == nil || *p.AudienceRating != 7.5 || p.DurationMS == nil || !slices.Equal(p.Tags, []string{"keep"}) {
		t.Errorf("carried fields lost: %+v", p)
	}
}

func TestMergePluginMetadata_OverrideReplacesButNeverLocked(t *testing.T) {
	item := baseItem()
	item.LockedFields = []string{media.FieldSummary, media.FieldSortTitle}
	p, changed, _ := mergePluginMetadata(item, []plugin.MetadataResult{override(plugin.MetadataResult{
		Title:   sp("Blade Runner: The Final Cut"),
		Summary: sp("plugin summary"),
		Year:    ip(2007),
	})})
	if p.Title != "Blade Runner: The Final Cut" || *p.Year != 2007 {
		t.Errorf("override not applied: %q %d", p.Title, *p.Year)
	}
	if p.SortTitle != "Blade Runner" {
		t.Errorf("locked sort title changed to %q", p.SortTitle)
	}
	if *p.Summary != "TMDB summary" {
		t.Errorf("locked summary overridden: %q", *p.Summary)
	}
	if slices.Contains(changed, media.FieldSummary) {
		t.Errorf("locked field reported changed: %v", changed)
	}
}

func TestMergePluginMetadata_LockedTitleKeepsSortTitle(t *testing.T) {
	item := baseItem()
	item.LockedFields = []string{media.FieldTitle}
	p, changed, _ := mergePluginMetadata(item, []plugin.MetadataResult{override(plugin.MetadataResult{Title: sp("Other")})})
	if p.Title != "Blade Runner" || p.SortTitle != "Blade Runner" || len(changed) != 0 {
		t.Errorf("locked title changed: %q / %q / %v", p.Title, p.SortTitle, changed)
	}
}

func TestMergePluginMetadata_PluginPrecedence(t *testing.T) {
	item := baseItem()
	item.Summary = nil
	p, _, _ := mergePluginMetadata(item, []plugin.MetadataResult{
		fill(plugin.MetadataResult{Summary: sp("first fill"), Tagline: sp("first tagline")}),
		fill(plugin.MetadataResult{Summary: sp("second fill")}),
		override(plugin.MetadataResult{Tagline: sp("override one")}),
		override(plugin.MetadataResult{Tagline: sp("override two")}),
	})
	if *p.Summary != "first fill" {
		t.Errorf("first fill should win, got %q", *p.Summary)
	}
	if *p.Tagline != "override two" {
		t.Errorf("last override should win, got %q", *p.Tagline)
	}
}

func TestMergePluginMetadata_ProviderIDsAndPosterFillOnly(t *testing.T) {
	item := baseItem()
	item.PosterPath = sp("Movies/poster.jpg")
	p, changed, poster := mergePluginMetadata(item, []plugin.MetadataResult{override(plugin.MetadataResult{
		TMDBID:    ip(999),
		AniListID: ip(42),
		PosterURL: "https://img.example.com/p.jpg",
	})})
	if p.TMDBID != nil {
		t.Errorf("existing tmdb_id re-pointed to %d", *p.TMDBID)
	}
	if p.AniListID == nil || *p.AniListID != 42 {
		t.Errorf("empty anilist_id not filled: %v", p.AniListID)
	}
	if poster != "" {
		t.Errorf("poster replaced an existing one: %q", poster)
	}
	if !slices.Equal(changed, []string{"anilist_id"}) {
		t.Errorf("changed = %v", changed)
	}

	item.PosterPath = nil
	if _, _, poster := mergePluginMetadata(item, []plugin.MetadataResult{
		{PosterURL: "https://a.example.com/1.jpg"}, {PosterURL: "https://b.example.com/2.jpg"},
	}); poster != "https://a.example.com/1.jpg" {
		t.Errorf("first poster should win, got %q", poster)
	}
}

func TestMergePluginMetadata_NoChange(t *testing.T) {
	item := baseItem()
	_, changed, _ := mergePluginMetadata(item, []plugin.MetadataResult{override(plugin.MetadataResult{
		Summary: sp("TMDB summary"), Year: ip(1982),
	})})
	if len(changed) != 0 {
		t.Errorf("identical values reported as changed: %v", changed)
	}
}

func TestEnrich_RunsPluginsAfterBuiltInAgents(t *testing.T) {
	updater := newMockUpdater()
	item := &media.Item{ID: uuid.New(), Type: "movie", Title: "Arrival", SortTitle: "Arrival",
		LockedFields: []string{media.FieldRating}}
	updater.items[item.ID] = item
	art := &mockArtwork{posterPath: "/media/Movies/Arrival/" + item.ID.String() + "-poster.jpg"}
	agent := &mockAgent{searchMovieResult: &metadata.MovieResult{
		TMDBID: 329865, Title: "Arrival", Year: 2016, Summary: "TMDB summary",
	}}
	e := newTestEnricher(agent, updater, art)
	plugins := &fakeMetadataPlugins{results: []plugin.MetadataResult{fill(plugin.MetadataResult{
		PluginName: "p",
		Summary:    sp("plugin summary"), // TMDB already set it: kept
		Tagline:    sp("Why are they here?"),
		Rating:     fp(9), // locked: ignored
		PosterURL:  "https://img.example.com/arrival.jpg",
	})}}
	e.SetMetadataPlugins(plugins)

	file := &media.File{FilePath: "/media/Movies/Arrival/Arrival.2016.mkv"}
	if err := e.Enrich(context.Background(), item, file); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(plugins.reqs) != 1 {
		t.Fatalf("plugins called %d times", len(plugins.reqs))
	}
	req := plugins.reqs[0]
	if req.ProviderIDs.TMDB != 329865 || req.FileName != "Arrival.2016.mkv" || req.ItemType != "movie" ||
		!slices.Equal(req.LockedFields, []string{media.FieldRating}) {
		t.Errorf("request did not see the built-in result: %+v", req)
	}
	if len(updater.updateCalls) < 2 {
		t.Fatalf("expected the built-in and the plugin update, got %d", len(updater.updateCalls))
	}
	last := updater.updateCalls[len(updater.updateCalls)-1]
	if last.Summary == nil || *last.Summary != "TMDB summary" {
		t.Errorf("fill replaced the TMDB summary: %v", last.Summary)
	}
	if last.Tagline == nil || *last.Tagline != "Why are they here?" {
		t.Errorf("tagline not filled: %v", last.Tagline)
	}
	if last.Rating != nil {
		t.Errorf("locked rating written: %v", *last.Rating)
	}
	if last.PosterPath == nil || *last.PosterPath != "Movies/Arrival/"+item.ID.String()+"-poster.jpg" {
		t.Errorf("plugin poster path = %v", last.PosterPath)
	}
}

func TestEnrich_PluginPassRunsWithoutAgentAndSurvivesFailures(t *testing.T) {
	updater := newMockUpdater()
	item := &media.Item{ID: uuid.New(), Type: "movie", Title: "Home Movie", SortTitle: "Home Movie"}
	updater.items[item.ID] = item
	e := NewEnricher(func() metadata.Agent { return nil }, &mockArtwork{posterErr: errors.New("blocked by safehttp")},
		updater, func() []string { return []string{"/media"} }, slog.New(slog.DiscardHandler))
	e.SetMetadataPlugins(&fakeMetadataPlugins{results: []plugin.MetadataResult{fill(plugin.MetadataResult{
		Summary: sp("from plugin"), PosterURL: "https://img.example.com/x.jpg",
	})}})

	if err := e.Enrich(context.Background(), item, &media.File{FilePath: "/media/Movies/home.mkv"}); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	if len(updater.updateCalls) != 1 || *updater.updateCalls[0].Summary != "from plugin" {
		t.Fatalf("plugin pass did not run without an agent: %+v", updater.updateCalls)
	}
	if updater.updateCalls[0].PosterPath != nil {
		t.Error("failed poster download still wrote a path")
	}

	// A panicking plugin pass is contained.
	e.SetMetadataPlugins(&fakeMetadataPlugins{panics: true})
	if err := e.Enrich(context.Background(), item, &media.File{FilePath: "/media/Movies/home.mkv"}); err != nil {
		t.Fatalf("Enrich after panic: %v", err)
	}
}

func TestEnrich_PluginPassSkipsPhotos(t *testing.T) {
	updater := newMockUpdater()
	item := &media.Item{ID: uuid.New(), Type: "photo", Title: "IMG_0001"}
	updater.items[item.ID] = item
	plugins := &fakeMetadataPlugins{}
	e := newTestEnricher(nil, updater, nil)
	e.agentFn = func() metadata.Agent { return nil }
	e.SetMetadataPlugins(plugins)
	_ = e.Enrich(context.Background(), item, &media.File{FilePath: "/media/Photos/IMG_0001.jpg"})
	if len(plugins.reqs) != 0 {
		t.Error("photos should not be sent to metadata plugins")
	}
}

func TestPluginRequest_ParentsAndGrandparents(t *testing.T) {
	updater := newMockUpdater()
	show := &media.Item{ID: uuid.New(), Type: "show", Title: "Severance"}
	season := &media.Item{ID: uuid.New(), Type: "season", Title: "Season 2", Index: ip(2), ParentID: &show.ID}
	ep := &media.Item{ID: uuid.New(), Type: "episode", Title: "Hello, Ms. Cobel", Index: ip(1), ParentID: &season.ID}
	for _, it := range []*media.Item{show, season, ep} {
		updater.items[it.ID] = it
	}
	e := newTestEnricher(nil, updater, nil)
	req := e.pluginRequest(context.Background(), ep, "/media/TV/Severance/Season 02/Severance.S02E01.mkv")
	if req.ParentTitle != "Season 2" || req.ParentIndex == nil || *req.ParentIndex != 2 ||
		req.GrandparentTitle != "Severance" || *req.Index != 1 || req.FileName != "Severance.S02E01.mkv" {
		t.Errorf("request = %+v", req)
	}
}
