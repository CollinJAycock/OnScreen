package scanner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
	"github.com/onscreen/onscreen/internal/metadata/nfo"
)

// extrasArtwork is mockArtwork plus the ExtraArtDownloader capability,
// backed by a real directory: a "download" writes a small file.
type extrasArtwork struct {
	mockArtwork
	downloads []string // absolute paths written
}

func (a *extrasArtwork) ArtFileExists(_ context.Context, absPath string) bool {
	info, err := os.Stat(absPath)
	return err == nil && info.Size() > 0
}

func (a *extrasArtwork) DownloadArtFile(_ context.Context, url, absPath string, force bool) (string, error) {
	if !force {
		if _, err := os.Stat(absPath); err == nil {
			return absPath, nil
		}
	}
	if err := os.WriteFile(absPath, []byte("img:"+url), 0o644); err != nil {
		return "", err
	}
	a.downloads = append(a.downloads, absPath)
	return absPath, nil
}

type fakeExtras struct {
	saveNFO  bool
	lang     string
	art      map[uuid.UUID][3]*string // logo, banner, disc
	studios  map[uuid.UUID][]metadata.Studio
	kinds    map[uuid.UUID]string
	setCalls int
}

func newFakeExtras(saveNFO bool) *fakeExtras {
	return &fakeExtras{saveNFO: saveNFO, lang: "en",
		art: map[uuid.UUID][3]*string{}, studios: map[uuid.UUID][]metadata.Studio{}, kinds: map[uuid.UUID]string{}}
}

func (f *fakeExtras) SetItemExtraArt(_ context.Context, id uuid.UUID, logo, banner, disc *string) error {
	cur := f.art[id]
	for i, v := range []*string{logo, banner, disc} {
		if v != nil {
			cur[i] = v
		}
	}
	f.art[id] = cur
	return nil
}

func (f *fakeExtras) SetItemStudios(_ context.Context, id uuid.UUID, kind string, list []metadata.Studio) error {
	f.setCalls++
	f.studios[id] = list
	f.kinds[id] = kind
	return nil
}

func (f *fakeExtras) ItemStudioNames(_ context.Context, id uuid.UUID) ([]string, error) {
	var out []string
	for _, s := range f.studios[id] {
		out = append(out, s.Name)
	}
	return out, nil
}

func (f *fakeExtras) LibraryNFOOptions(context.Context, uuid.UUID) (bool, string, error) {
	return f.saveNFO, f.lang, nil
}

type fakeExtraArtAgent struct {
	movie, show metadata.ExtraArt
	calls       int
	lastLang    string
}

func (a *fakeExtraArtAgent) MovieArt(_ context.Context, _ int, _, lang string) (metadata.ExtraArt, error) {
	a.calls++
	a.lastLang = lang
	return a.movie, nil
}
func (a *fakeExtraArtAgent) ShowArt(_ context.Context, _ int, lang string) (metadata.ExtraArt, error) {
	a.calls++
	a.lastLang = lang
	return a.show, nil
}
func (a *fakeExtraArtAgent) ArtistArt(context.Context, uuid.UUID, string) (metadata.ExtraArt, error) {
	a.calls++
	return metadata.ExtraArt{}, nil
}
func (a *fakeExtraArtAgent) AlbumArt(context.Context, uuid.UUID, string) (metadata.ExtraArt, error) {
	a.calls++
	return metadata.ExtraArt{}, nil
}

func newExtrasEnricher(t *testing.T, root string, agent metadata.Agent, updater *mockUpdater, art *extrasArtwork, extras *fakeExtras, fan *fakeExtraArtAgent) *Enricher {
	t.Helper()
	e := NewEnricher(func() metadata.Agent { return agent }, art, updater, func() []string { return []string{root} }, slog.Default())
	e.SetItemExtras(extras)
	e.SetExtraArtFn(func() metadata.ExtraArtAgent {
		if fan == nil {
			return nil
		}
		return fan
	})
	return e
}

func TestEnrichMovieExtrasLocalFirstThenFanartAndNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Alien (1979)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	moviePath := filepath.Join(dir, "Alien (1979).mkv")
	// The operator's own clear logo wins over fanart.tv.
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Alien"}
	updater.files[itemID] = []media.File{{ID: uuid.New(), MediaItemID: itemID, FilePath: moviePath, Status: "active"}}
	agent := &mockAgent{searchMovieResult: &metadata.MovieResult{
		TMDBID: 348, IMDBID: "tt0078748", Title: "Alien", Year: 1979,
		Studios: []metadata.Studio{{Name: "Brandywine Productions", TMDBID: 401}, {Name: "20th Century Fox", TMDBID: 25}},
	}}
	art := &extrasArtwork{}
	extras := newFakeExtras(true)
	fan := &fakeExtraArtAgent{movie: metadata.ExtraArt{
		LogoURL:   "https://assets.fanart.tv/logo.png",
		BannerURL: "https://assets.fanart.tv/banner.jpg",
		DiscURL:   "https://assets.fanart.tv/disc.png",
	}}
	e := newExtrasEnricher(t, root, agent, updater, art, extras, fan)

	if err := e.Enrich(context.Background(), updater.items[itemID], &updater.files[itemID][0]); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	got := extras.art[itemID]
	if got[0] == nil || *got[0] != "Alien (1979)/logo.png" {
		t.Errorf("logo = %v, want the local logo.png", strp(got[0]))
	}
	if got[1] == nil || *got[1] != "Alien (1979)/banner.jpg" {
		t.Errorf("banner = %v, want the downloaded banner.jpg", strp(got[1]))
	}
	if got[2] == nil || *got[2] != "Alien (1979)/disc.png" {
		t.Errorf("disc = %v, want the downloaded disc.png", strp(got[2]))
	}
	for _, d := range art.downloads {
		if strings.HasSuffix(d, "clearlogo.png") {
			t.Errorf("downloaded a logo although logo.png is next to the movie")
		}
	}
	if fan.lastLang != "en" {
		t.Errorf("fanart.tv asked in %q, want the library language", fan.lastLang)
	}
	if extras.kinds[itemID] != "studio" || len(extras.studios[itemID]) != 2 {
		t.Errorf("studios = %v (%s)", extras.studios[itemID], extras.kinds[itemID])
	}

	// save_nfo: <basename>.nfo written, ours, with the studios.
	nfoPath := filepath.Join(dir, "Alien (1979).nfo")
	data, err := os.ReadFile(nfoPath)
	if err != nil {
		t.Fatalf("NFO not written: %v", err)
	}
	if !strings.Contains(string(data), nfo.Marker) || !strings.Contains(string(data), "<studio>20th Century Fox</studio>") {
		t.Errorf("NFO content:\n%s", data)
	}

	// A second enrichment reads our own NFO as ids only: the provider's
	// fresh title lands instead of being pinned to the NFO's.
	agent.searchMovieResult.Title = "Alien (Director's Cut)"
	agent.searchMovieResult.Studios = []metadata.Studio{{Name: "Brandywine Productions"}}
	if err := e.Enrich(context.Background(), updater.items[itemID], &updater.files[itemID][0]); err != nil {
		t.Fatal(err)
	}
	last := updater.updateCalls[len(updater.updateCalls)-1]
	if last.Title != "Alien (Director's Cut)" {
		t.Errorf("title = %q; our own NFO must not override the provider", last.Title)
	}
	if len(extras.studios[itemID]) != 1 {
		t.Errorf("studios = %v; our own NFO's studios must not win", extras.studios[itemID])
	}
}

func TestEnrichMovieNeverOverwritesForeignNFO(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Heat (1995)")
	_ = os.MkdirAll(dir, 0o755)
	moviePath := filepath.Join(dir, "Heat (1995).mkv")
	foreign := []byte(`<?xml version="1.0"?><movie><title>Heat</title><studio>Forward Pass</studio></movie>`)
	nfoPath := filepath.Join(dir, "movie.nfo")
	if err := os.WriteFile(nfoPath, foreign, 0o644); err != nil {
		t.Fatal(err)
	}

	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Heat"}
	updater.files[itemID] = []media.File{{MediaItemID: itemID, FilePath: moviePath, Status: "active"}}
	agent := &mockAgent{searchMovieResult: &metadata.MovieResult{TMDBID: 949, Title: "Heat",
		Studios: []metadata.Studio{{Name: "Warner Bros. Pictures"}}}}
	extras := newFakeExtras(true)
	e := newExtrasEnricher(t, root, agent, updater, &extrasArtwork{}, extras, nil)

	if err := e.Enrich(context.Background(), updater.items[itemID], &updater.files[itemID][0]); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(nfoPath); string(b) != string(foreign) {
		t.Errorf("foreign movie.nfo was modified:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "Heat (1995).nfo")); err == nil {
		t.Error("wrote a second NFO beside the operator's movie.nfo")
	}
	// The operator's NFO <studio> wins over TMDB's companies.
	if s := extras.studios[itemID]; len(s) != 1 || s[0].Name != "Forward Pass" {
		t.Errorf("studios = %v, want the NFO's", s)
	}
}

func TestSaveNFOOffWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Film")
	_ = os.MkdirAll(dir, 0o755)
	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Film"}
	updater.files[itemID] = []media.File{{MediaItemID: itemID, FilePath: filepath.Join(dir, "Film.mkv"), Status: "active"}}
	agent := &mockAgent{searchMovieResult: &metadata.MovieResult{TMDBID: 1, Title: "Film"}}
	e := newExtrasEnricher(t, root, agent, updater, &extrasArtwork{}, newFakeExtras(false), nil)
	if err := e.Enrich(context.Background(), updater.items[itemID], &updater.files[itemID][0]); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("save_nfo off but the folder has %d new files", len(entries))
	}

	// Object storage: no NFO even with save_nfo on.
	e = newExtrasEnricher(t, root, agent, updater, &extrasArtwork{}, newFakeExtras(true), nil)
	e.SetLocalMediaFn(func() bool { return false })
	e.WriteItemNFO(context.Background(), itemID)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("remote media store but an NFO was written")
	}
}

func TestEnrichShowAniListBannerFallbackAndNetworks(t *testing.T) {
	root := t.TempDir()
	showDir := filepath.Join(root, "Frieren")
	seasonDir := filepath.Join(showDir, "Season 01")
	_ = os.MkdirAll(seasonDir, 0o755)
	epPath := filepath.Join(seasonDir, "Frieren - S01E01.mkv")

	showID := uuid.New()
	updater := newMockUpdater()
	updater.items[showID] = &media.Item{ID: showID, Type: "show", Title: "Frieren"}
	updater.files[showID] = nil
	agent := &mockAgent{searchTVResult: &metadata.TVShowResult{
		TMDBID: 209867, TVDBID: 424536, Title: "Frieren",
		BannerURL: "https://s4.anilist.co/banner.jpg",
		Studios:   []metadata.Studio{{Name: "Nippon TV", TMDBID: 57}}, StudiosChecked: true,
	}}
	art := &extrasArtwork{}
	extras := newFakeExtras(true)
	fan := &fakeExtraArtAgent{show: metadata.ExtraArt{LogoURL: "https://assets.fanart.tv/frieren.png"}}
	e := newExtrasEnricher(t, root, agent, updater, art, extras, fan)

	file := &media.File{FilePath: epPath, Status: "active"}
	if err := e.enrichShow(context.Background(), agent, updater.items[showID], file); err != nil {
		t.Fatal(err)
	}
	got := extras.art[showID]
	if got[0] == nil || *got[0] != "Frieren/clearlogo.png" {
		t.Errorf("logo = %v", strp(got[0]))
	}
	if got[1] == nil || *got[1] != "Frieren/banner.jpg" {
		t.Errorf("banner = %v, want AniList's banner as the fallback", strp(got[1]))
	}
	if got[2] != nil {
		t.Errorf("shows have no disc art, got %v", *got[2])
	}
	if extras.kinds[showID] != "network" || len(extras.studios[showID]) != 1 {
		t.Errorf("networks = %v (%s)", extras.studios[showID], extras.kinds[showID])
	}
	// tvshow.nfo needs a descendant file; register one and write. (The
	// mock updater doesn't keep tvdb_id; the database would.)
	tvdb := 424536
	updater.items[showID].TVDBID = &tvdb
	epID := uuid.New()
	updater.children[showID] = []media.Item{{ID: epID, Type: "episode"}}
	updater.files[epID] = []media.File{{MediaItemID: epID, FilePath: epPath, Status: "active"}}
	e.WriteItemNFO(context.Background(), showID)
	data, err := os.ReadFile(filepath.Join(showDir, "tvshow.nfo"))
	if err != nil || !strings.Contains(string(data), "<studio>Nippon TV</studio>") ||
		!strings.Contains(string(data), `<uniqueid type="tvdb" default="true">424536</uniqueid>`) {
		t.Errorf("tvshow.nfo = %s, %v", data, err)
	}
}

func TestMovieInLibraryRootGetsNoFolderArt(t *testing.T) {
	root := t.TempDir()
	itemID := uuid.New()
	updater := newMockUpdater()
	updater.items[itemID] = &media.Item{ID: itemID, Type: "movie", Title: "Loose"}
	updater.files[itemID] = []media.File{{MediaItemID: itemID, FilePath: filepath.Join(root, "Loose.mkv"), Status: "active"}}
	agent := &mockAgent{searchMovieResult: &metadata.MovieResult{TMDBID: 7, Title: "Loose"}}
	art := &extrasArtwork{}
	fan := &fakeExtraArtAgent{movie: metadata.ExtraArt{LogoURL: "https://assets.fanart.tv/x.png"}}
	e := newExtrasEnricher(t, root, agent, updater, art, newFakeExtras(false), fan)
	if err := e.Enrich(context.Background(), updater.items[itemID], &updater.files[itemID][0]); err != nil {
		t.Fatal(err)
	}
	if len(art.downloads) != 0 || fan.calls != 0 {
		t.Errorf("a movie in the library root got folder art: %v (fanart calls %d)", art.downloads, fan.calls)
	}
}

func TestArtExt(t *testing.T) {
	for in, want := range map[string]string{
		"https://x/a.PNG":         ".png",
		"https://x/a.jpeg?x=1":    ".jpg",
		"https://x/a":             ".def",
		"https://x/a.gif":         ".def",
		"https://x/dir.png/a.jpg": ".jpg",
	} {
		if got := artExt(in, ".def"); got != want {
			t.Errorf("artExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsOwnNFOWrite(t *testing.T) {
	dir := t.TempDir()
	ours, _ := nfo.MarshalMovie(&nfo.Movie{Title: "x"})
	_ = os.WriteFile(filepath.Join(dir, "ours.nfo"), ours, 0o644)
	_ = os.WriteFile(filepath.Join(dir, "theirs.nfo"), []byte("<movie/>"), 0o644)
	if !isOwnNFOWrite(filepath.Join(dir, "ours.nfo")) || isOwnNFOWrite(filepath.Join(dir, "theirs.nfo")) {
		t.Error("watcher NFO filter misjudged a file")
	}
	if !isOwnNFOWrite(filepath.Join(dir, ".movie.nfo.123"+nfo.TempSuffix)) {
		t.Error("watcher should ignore the NFO writer's temp file")
	}
	if isOwnNFOWrite(filepath.Join(dir, "Film.mkv")) {
		t.Error("watcher must not ignore media files")
	}
}

func strp(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}
