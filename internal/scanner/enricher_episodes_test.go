package scanner

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/onscreen/onscreen/internal/domain/media"
	"github.com/onscreen/onscreen/internal/metadata"
)

// numberedAgent answers GetEpisode per (season, episode) and, when
// orderings is set, implements metadata.EpisodeOrderProvider the way the
// TMDB client does.
type numberedAgent struct {
	mockAgent
	episodes      map[[2]int]metadata.EpisodeResult
	episodeCalls  [][2]int
	orderings     map[string]*metadata.EpisodeOrdering
	orderingCalls []string
}

func (a *numberedAgent) GetEpisode(_ context.Context, _, season, episode int) (*metadata.EpisodeResult, error) {
	a.episodeCalls = append(a.episodeCalls, [2]int{season, episode})
	if r, ok := a.episodes[[2]int{season, episode}]; ok {
		return &r, nil
	}
	return nil, errors.New("not found")
}

func (a *numberedAgent) GetEpisodeOrdering(_ context.Context, _ int, order string) (*metadata.EpisodeOrdering, error) {
	a.orderingCalls = append(a.orderingCalls, order)
	if o, ok := a.orderings[order]; ok {
		return o, nil
	}
	return nil, fmt.Errorf("no %s group: %w", order, metadata.ErrEpisodeOrderUnavailable)
}

// airedAgent is numberedAgent without the ordering method — a provider
// that has no alternative orders at all.
type airedAgent struct{ mockAgent }

type stubLibOrder struct {
	order string
	calls int
}

func (s *stubLibOrder) GetLibraryEpisodeOrder(_ context.Context, _ uuid.UUID) (string, error) {
	s.calls++
	return s.order, nil
}

// showFixture seeds show → season → episode and returns the episode.
func showFixture(updater *mockUpdater, showOrder *string, seasonNum, index int, indexEnd *int) (*media.Item, *media.Item) {
	tmdbID := 1396
	showID, seasonID, epID := uuid.New(), uuid.New(), uuid.New()
	poster := "shows/x/poster.jpg"
	updater.items[showID] = &media.Item{ID: showID, LibraryID: uuid.New(), Type: "show", Title: "Show", TMDBID: &tmdbID, PosterPath: &poster, EpisodeOrder: showOrder}
	updater.items[seasonID] = &media.Item{ID: seasonID, Type: "season", Title: "Season", ParentID: &showID, Index: &seasonNum}
	idx := index
	updater.items[epID] = &media.Item{ID: epID, Type: "episode", Title: defaultEpisodeTitle(index, derefOr(indexEnd)), ParentID: &seasonID, Index: &idx, IndexEnd: indexEnd}
	return updater.items[showID], updater.items[epID]
}

func derefOr(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func TestJoinEpisodeTitles(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"Pilot"}, "Pilot"},
		{[]string{"Pilot", "Cat's in the Bag..."}, "Pilot / Cat's in the Bag..."},
		{[]string{"The Best of Both Worlds (1)", "The Best of Both Worlds (2)"}, "The Best of Both Worlds (1–2)"},
		{[]string{"Pilot, Part 1", "Pilot, Part 2"}, "Pilot (Parts 1–2)"},
		{[]string{"Pilot Part 1", "Pilot Part 2", "Pilot Part 3"}, "Pilot (Parts 1–3)"},
		{[]string{"Pilot Pt. 1", "Pilot Pt. 2"}, "Pilot (Parts 1–2)"},
		// Not consecutive, or different bases: plain join.
		{[]string{"Pilot (1)", "Pilot (3)"}, "Pilot (1) / Pilot (3)"},
		{[]string{"Alpha (1)", "Beta (2)"}, "Alpha (1) / Beta (2)"},
		{[]string{"Pilot (1)", "Pilot, Part 2"}, "Pilot (1) / Pilot, Part 2"},
		// Empty and repeated titles.
		{[]string{"", "Second"}, "Second"},
		{[]string{"Same", "Same"}, "Same"},
		{[]string{"", ""}, ""},
	}
	for _, tt := range tests {
		if got := joinEpisodeTitles(tt.in); got != tt.want {
			t.Errorf("joinEpisodeTitles(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestEnrichEpisode_MultiEpisodeJoinsTitles: an E01–E02 item looks both
// episodes up, joins their titles and takes the rest from the first.
func TestEnrichEpisode_MultiEpisodeJoinsTitles(t *testing.T) {
	agent := &numberedAgent{episodes: map[[2]int]metadata.EpisodeResult{
		{1, 1}: {Title: "Encounter at Farpoint (1)", Summary: "First half.", ThumbURL: "http://x/1.jpg"},
		{1, 2}: {Title: "Encounter at Farpoint (2)", Summary: "Second half."},
	}}
	updater := newMockUpdater()
	two := 2
	_, ep := showFixture(updater, nil, 1, 1, &two)
	e := newTestEnricher(agent, updater, &mockArtwork{thumbPath: "/media/shows/x/thumb.jpg"})

	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/S01E01-E02.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if len(updater.updateCalls) != 1 {
		t.Fatalf("update calls = %d, want 1", len(updater.updateCalls))
	}
	p := updater.updateCalls[0]
	if p.Title != "Encounter at Farpoint (1–2)" {
		t.Errorf("title = %q", p.Title)
	}
	if p.Summary == nil || *p.Summary != "First half." {
		t.Errorf("summary = %v, want the first episode's", p.Summary)
	}
	if p.ThumbPath == nil {
		t.Errorf("thumb from the first episode not applied")
	}
	if len(agent.episodeCalls) != 2 {
		t.Errorf("episode lookups = %v, want E01 and E02", agent.episodeCalls)
	}
}

// TestEnrichSeasonChildren_MultiEpisodeUsesBulkFetch: the season's bulk
// data covers every episode of a range item; no per-episode round-trips.
func TestEnrichSeasonChildren_MultiEpisodeUsesBulkFetch(t *testing.T) {
	agent := &numberedAgent{}
	updater := newMockUpdater()
	two := 2
	show, ep := showFixture(updater, nil, 1, 1, &two)
	season := updater.items[*ep.ParentID]
	updater.children[season.ID] = []media.Item{*ep}
	e := newTestEnricher(agent, updater, nil)

	preloaded := []metadata.EpisodeResult{
		{EpisodeNum: 1, Title: "Pilot"},
		{EpisodeNum: 2, Title: "Second"},
	}
	e.enrichSeasonChildren(context.Background(), agent, show, season, &media.File{FilePath: "/media/shows/x/a.mkv"}, preloaded)
	if len(agent.episodeCalls) != 0 {
		t.Errorf("per-episode lookups = %v, want none", agent.episodeCalls)
	}
	if len(updater.updateCalls) != 1 || updater.updateCalls[0].Title != "Pilot / Second" {
		t.Fatalf("updates = %+v, want one titled %q", updater.updateCalls, "Pilot / Second")
	}
}

func dvdOrdering() *metadata.EpisodeOrdering {
	return &metadata.EpisodeOrdering{Order: metadata.EpisodeOrderDVD, Episodes: []metadata.OrderedEpisode{
		{Season: 1, Number: 1, Absolute: 1, Episode: metadata.EpisodeResult{Title: "Serenity", Summary: "The real pilot."}},
		{Season: 1, Number: 2, Absolute: 2, Episode: metadata.EpisodeResult{Title: "The Train Job"}},
	}}
}

// TestEnrichEpisode_DVDOrder: a show set to DVD order looks a file's
// SxxEyy up in the DVD group, not in aired order.
func TestEnrichEpisode_DVDOrder(t *testing.T) {
	agent := &numberedAgent{
		episodes:  map[[2]int]metadata.EpisodeResult{{1, 1}: {Title: "The Train Job (aired first)"}},
		orderings: map[string]*metadata.EpisodeOrdering{metadata.EpisodeOrderDVD: dvdOrdering()},
	}
	updater := newMockUpdater()
	dvd := metadata.EpisodeOrderDVD
	_, ep := showFixture(updater, &dvd, 1, 1, nil)
	e := newTestEnricher(agent, updater, nil)

	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/S01E01.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if got := updater.updateCalls[0].Title; got != "Serenity" {
		t.Errorf("title = %q, want the DVD-order episode", got)
	}
	if len(agent.episodeCalls) != 0 {
		t.Errorf("aired lookups = %v, want none", agent.episodeCalls)
	}
}

// TestEnrichEpisode_LibraryDefaultOrder: a show with no order of its own
// follows the library's; a DVD+range item resolves both episodes in the
// ordering, which is fetched once.
func TestEnrichEpisode_LibraryDefaultOrder(t *testing.T) {
	agent := &numberedAgent{orderings: map[string]*metadata.EpisodeOrdering{metadata.EpisodeOrderDVD: dvdOrdering()}}
	updater := newMockUpdater()
	two := 2
	_, ep := showFixture(updater, nil, 1, 1, &two)
	e := newTestEnricher(agent, updater, nil)
	lib := &stubLibOrder{order: "dvd"}
	e.SetLibraryEpisodeOrderFn(func() LibraryEpisodeOrderer { return lib })

	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/S01E01-E02.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if got := updater.updateCalls[0].Title; got != "Serenity / The Train Job" {
		t.Errorf("title = %q", got)
	}
	if len(agent.orderingCalls) != 1 {
		t.Errorf("ordering fetched %d times, want once", len(agent.orderingCalls))
	}
}

// TestEnrichEpisode_AbsoluteOrder: "Show - 14" in a single season maps by
// absolute number; a special (season 0) keeps aired order.
func TestEnrichEpisode_AbsoluteOrder(t *testing.T) {
	ordering := &metadata.EpisodeOrdering{Order: metadata.EpisodeOrderAbsolute, Episodes: []metadata.OrderedEpisode{
		{Season: 1, Number: 14, Absolute: 14, Episode: metadata.EpisodeResult{SeasonNum: 2, EpisodeNum: 2, Title: "Season Two, Episode Two"}},
	}}
	agent := &numberedAgent{
		episodes:  map[[2]int]metadata.EpisodeResult{{0, 1}: {Title: "Aired Special"}},
		orderings: map[string]*metadata.EpisodeOrdering{metadata.EpisodeOrderAbsolute: ordering},
	}
	updater := newMockUpdater()
	abs := metadata.EpisodeOrderAbsolute
	_, ep := showFixture(updater, &abs, 1, 14, nil)
	e := newTestEnricher(agent, updater, nil)
	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/Show - 14.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if got := updater.updateCalls[0].Title; got != "Season Two, Episode Two" {
		t.Errorf("title = %q", got)
	}

	updater2 := newMockUpdater()
	_, special := showFixture(updater2, &abs, 0, 1, nil)
	e2 := newTestEnricher(agent, updater2, nil)
	if err := e2.enrichEpisode(context.Background(), agent, special, &media.File{FilePath: "/media/shows/x/S00E01.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode special: %v", err)
	}
	if got := updater2.updateCalls[0].Title; got != "Aired Special" {
		t.Errorf("special title = %q, want aired order", got)
	}
}

// TestEnrichEpisode_OrderUnavailableFallsBackToAired: no such group at the
// provider (or a provider without orders at all) → aired order, and the
// ordering is asked for once per show, not per episode.
func TestEnrichEpisode_OrderUnavailableFallsBackToAired(t *testing.T) {
	agent := &numberedAgent{episodes: map[[2]int]metadata.EpisodeResult{
		{1, 1}: {Title: "Aired One"},
		{1, 2}: {Title: "Aired Two"},
	}}
	updater := newMockUpdater()
	dvd := metadata.EpisodeOrderDVD
	show, ep1 := showFixture(updater, &dvd, 1, 1, nil)
	two := 2
	ep2ID := uuid.New()
	updater.items[ep2ID] = &media.Item{ID: ep2ID, Type: "episode", Title: "Episode 2", ParentID: ep1.ParentID, Index: &two}
	e := newTestEnricher(agent, updater, nil)

	for _, ep := range []*media.Item{ep1, updater.items[ep2ID]} {
		if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/a.mkv"}, nil); err != nil {
			t.Fatalf("enrichEpisode: %v", err)
		}
	}
	if updater.updateCalls[0].Title != "Aired One" || updater.updateCalls[1].Title != "Aired Two" {
		t.Errorf("titles = %q, %q; want aired order", updater.updateCalls[0].Title, updater.updateCalls[1].Title)
	}
	if len(agent.orderingCalls) != 1 {
		t.Errorf("ordering asked %d times, want once (miss cached)", len(agent.orderingCalls))
	}
	_ = show

	// A provider that can't list orders at all falls back too.
	plain := &airedAgent{mockAgent{getEpisodeResult: &metadata.EpisodeResult{Title: "Plain Aired"}}}
	updater3 := newMockUpdater()
	_, ep3 := showFixture(updater3, &dvd, 1, 1, nil)
	e3 := newTestEnricher(plain, updater3, nil)
	if err := e3.enrichEpisode(context.Background(), plain, ep3, &media.File{FilePath: "/media/shows/x/a.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if updater3.updateCalls[0].Title != "Plain Aired" {
		t.Errorf("title = %q", updater3.updateCalls[0].Title)
	}
}

// TestEnrichEpisode_AiredDefaultNeverAsksForOrdering: the default path —
// and so anime libraries, which keep aired order unless an admin picks
// another — never touches the ordering machinery.
func TestEnrichEpisode_AiredDefaultNeverAsksForOrdering(t *testing.T) {
	agent := &numberedAgent{
		episodes:  map[[2]int]metadata.EpisodeResult{{1, 1}: {Title: "Aired"}},
		orderings: map[string]*metadata.EpisodeOrdering{metadata.EpisodeOrderDVD: dvdOrdering()},
	}
	updater := newMockUpdater()
	_, ep := showFixture(updater, nil, 1, 1, nil)
	e := newTestEnricher(agent, updater, nil)
	lib := &stubLibOrder{order: "aired"}
	e.SetLibraryEpisodeOrderFn(func() LibraryEpisodeOrderer { return lib })
	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/a.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if updater.updateCalls[0].Title != "Aired" || len(agent.orderingCalls) != 0 {
		t.Errorf("title = %q, ordering calls = %d; want aired with no ordering calls",
			updater.updateCalls[0].Title, len(agent.orderingCalls))
	}
}

// TestEnrichEpisode_TVDBOrderingFallback: a show matched on TVDB only gets
// its DVD order from TVDB's season types.
func TestEnrichEpisode_TVDBOrderingFallback(t *testing.T) {
	agent := &airedAgent{}
	tv := &orderingTVDB{ordering: dvdOrdering()}
	updater := newMockUpdater()
	dvd := metadata.EpisodeOrderDVD
	show, ep := showFixture(updater, &dvd, 1, 2, nil)
	tvdbID := 73255
	show.TMDBID, show.TVDBID = nil, &tvdbID
	e := newTestEnricher(agent, updater, nil)
	e.SetTVDBFallbackFn(func() TVDBFallback { return tv })
	if err := e.enrichEpisode(context.Background(), agent, ep, &media.File{FilePath: "/media/shows/x/a.mkv"}, nil); err != nil {
		t.Fatalf("enrichEpisode: %v", err)
	}
	if updater.updateCalls[0].Title != "The Train Job" || tv.calls != 1 {
		t.Errorf("title = %q, tvdb ordering calls = %d", updater.updateCalls[0].Title, tv.calls)
	}
}

type orderingTVDB struct {
	mockTVDB
	ordering *metadata.EpisodeOrdering
	calls    int
}

func (o *orderingTVDB) GetEpisodeOrdering(_ context.Context, _ int, _ string) (*metadata.EpisodeOrdering, error) {
	o.calls++
	return o.ordering, nil
}
