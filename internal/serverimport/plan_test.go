package serverimport

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	planNow     = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	planHistory = planNow.AddDate(0, -24, 0)
)

func f64(f float64) *float64 { return &f }

// planSnapshot: an owner with full state and a second account known only
// from history, against testLibrary.
func planSnapshot() *Snapshot {
	s := newSnapshot(ServerInfo{ID: "srv"}, []Account{{ID: "1", Name: "Owner", FullState: true}, {ID: "2", Name: "Kid"}, {ID: "3", Name: "Guest"}})
	s.Items = map[string]*Item{
		"alien":  {Key: "alien", Kind: ItemMovie, Title: "Alien", IDs: ExternalIDs{TMDB: 348}, DurationMS: 6900000},
		"heat":   {Key: "heat", Kind: ItemMovie, Title: "Heat", IDs: ExternalIDs{IMDB: "tt0113277"}, DurationMS: 10200000},
		"sev101": {Key: "sev101", Kind: ItemEpisode, Title: "Good News", ShowIDs: ExternalIDs{TVDB: 371980}, Season: 1, Episode: 1, HasSE: true},
		"sev102": {Key: "sev102", Kind: ItemEpisode, Title: "Half Loop", ShowIDs: ExternalIDs{TVDB: 371980}, Season: 1, Episode: 2, HasSE: true},
		"show":   {Key: "show", Kind: ItemShow, Title: "Severance", IDs: ExternalIDs{TVDB: 371980}},
		"song":   {Key: "song", Kind: ItemTrack, Title: "Song", Path: "/m/Artist/Album/01 Song.flac"},
		"gone":   {Key: "gone", Kind: ItemMovie, Title: "Not Here"},
	}
	watched := time.Date(2025, 3, 1, 20, 0, 0, 0, time.UTC)
	s.States["1"] = []State{
		{Key: "alien", Played: true, LastViewedAt: watched, Rating: f64(11)},                                 // rating clamped to 10
		{Key: "heat", PositionMS: 600000, LastViewedAt: watched},                                             // in progress
		{Key: "sev101", Played: true, PositionMS: 60000, LastViewedAt: watched},                              // rewatching
		{Key: "sev102", Played: true},                                                                        // no date
		{Key: "show", Favorite: true, Rating: f64(7)},                                                        // a show: favorite + rating, no watch state
		{Key: "song", Rating: f64(8), Played: true, LastViewedAt: watched, PositionMS: 1000, Favorite: true}, // tracks have no watch state
		{Key: "gone", Played: true, LastViewedAt: watched},                                                   // not on this server
	}
	s.Plays = []Play{
		{Account: "1", Key: "alien", At: watched},
		{Account: "1", Key: "alien", At: planHistory.Add(-time.Hour)}, // older than retention
		{Account: "2", Key: "sev102", At: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)},
		{Account: "2", Key: "sev102", At: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Account: "2", Key: "song", At: watched},
		{Account: "2", Key: "gone", At: watched},
		{Account: "3", Key: "alien", At: watched}, // unmapped
	}
	s.Playlists["1"] = []Playlist{
		{ID: "p1", Name: "Mix", Keys: []string{"heat", "gone", "alien", "heat"}},
		{ID: "p2", Name: "Empty", Keys: []string{"gone"}},
	}
	return s
}

func TestBuildPlan(t *testing.T) {
	l := newTestLibrary()
	owner, kid := uuid.New(), uuid.New()
	all := Options{Watched: true, Ratings: true, Favorites: true, Playlists: true, History: true}
	p := BuildPlan(planSnapshot(), l.idx, map[string]uuid.UUID{"1": owner, "2": kid}, all, planHistory)

	watched := time.Date(2025, 3, 1, 20, 0, 0, 0, time.UTC)
	marks := map[[2]uuid.UUID]time.Time{}
	for _, m := range p.Marks {
		marks[[2]uuid.UUID{m.User, m.Media}] = m.At
	}
	wantMarks := map[[2]uuid.UUID]time.Time{
		{owner, l.alienHD}: watched, // every copy
		{owner, l.alien4K}: watched,
		{owner, l.sev101}:  watched.Add(-time.Second), // before the rewatch's resume point
		{owner, l.sev102}:  unknownViewedAt,
		{kid, l.sev102}:    time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC), // latest play
	}
	if len(marks) != len(wantMarks) {
		t.Errorf("marks = %v", p.Marks)
	}
	for k, at := range wantMarks {
		if got, ok := marks[k]; !ok || !got.Equal(at) {
			t.Errorf("mark %v = %v (present %v), want %v", k, got, ok, at)
		}
	}

	var resumes, plays []Event
	for _, e := range p.Events {
		if e.Type == eventResume {
			resumes = append(resumes, e)
		} else {
			plays = append(plays, e)
		}
	}
	if len(resumes) != 2 {
		t.Fatalf("resume points = %+v", resumes)
	}
	for _, e := range resumes {
		switch e.Media {
		case l.heat:
			// No duration here: the source's.
			if e.PositionMS != 600000 || e.DurationMS != 10200000 || !e.At.Equal(watched) {
				t.Errorf("Heat resume = %+v", e)
			}
		case l.sev101:
			if e.PositionMS != 60000 || e.DurationMS != 3000000 {
				t.Errorf("rewatch resume = %+v", e)
			}
		default:
			t.Errorf("unexpected resume point %+v", e)
		}
	}
	// Plays: the owner's Alien once (on the best match, not every copy),
	// the kid's two Half Loop plays and their song; the old play and the
	// unmapped account's are out.
	if len(plays) != 4 {
		t.Fatalf("plays = %+v", plays)
	}
	if plays[0].Media != l.alienHD || plays[0].PositionMS != 7000000 || plays[0].DurationMS != 7000000 {
		t.Errorf("Alien play = %+v (duration should come from this server's item)", plays[0])
	}
	for _, e := range plays {
		if e.Media == l.alien4K {
			t.Error("a play was recorded on the second copy too")
		}
	}
	if p.Stats.PlaysOutsideRetention != 1 {
		t.Errorf("plays outside retention = %d", p.Stats.PlaysOutsideRetention)
	}

	ratings := map[uuid.UUID]float64{}
	for _, r := range p.Ratings {
		ratings[r.Media] = r.Score
	}
	if ratings[l.alienHD] != 10 || ratings[l.alien4K] != 10 || ratings[l.severance] != 7 || ratings[l.track] != 8 || len(ratings) != 4 {
		t.Errorf("ratings = %v", ratings)
	}
	favs := map[uuid.UUID]bool{}
	for _, f := range p.Favorites {
		favs[f.Media] = true
	}
	if !favs[l.severance] || !favs[l.track] || len(favs) != 2 {
		t.Errorf("favorites = %v", favs)
	}

	if len(p.Playlists) != 1 {
		t.Fatalf("playlists = %+v (one with no matches is skipped)", p.Playlists)
	}
	pl := p.Playlists[0]
	if pl.User != owner || pl.Name != "Mix" || pl.SourceKey != "srv:1:p1" || !sameIDs(pl.Items, ids(l.heat, l.alienHD)) {
		t.Errorf("playlist = %+v", pl)
	}

	if p.Stats.Unmatched != 1 || len(p.Unmatched) != 1 || p.Unmatched[0] != "Not Here (movie)" {
		t.Errorf("unmatched = %d %v", p.Stats.Unmatched, p.Unmatched)
	}
	c := p.Stats.Planned
	if c.Watched != 5 || c.ResumePoints != 2 || c.Plays != 4 || c.Ratings != 4 || c.Favorites != 2 || c.Playlists != 1 || c.PlaylistItems != 2 {
		t.Errorf("planned = %+v", c)
	}
	if p.Stats.SourceItems != 7 || p.Stats.Matched+p.Stats.Unmatched != p.Stats.Referenced {
		t.Errorf("stats = %+v", p.Stats)
	}
}

func TestBuildPlan_OnlyWhatsChosen(t *testing.T) {
	l := newTestLibrary()
	owner := uuid.New()
	p := BuildPlan(planSnapshot(), l.idx, map[string]uuid.UUID{"1": owner}, Options{Ratings: true}, planHistory)
	if len(p.Marks)+len(p.Events)+len(p.Favorites)+len(p.Playlists) != 0 || len(p.Ratings) != 4 {
		t.Errorf("ratings-only plan: %d marks, %d events, %d ratings, %d favorites, %d playlists",
			len(p.Marks), len(p.Events), len(p.Ratings), len(p.Favorites), len(p.Playlists))
	}
}

func TestBuildPlan_HistoryWithoutWatched(t *testing.T) {
	l := newTestLibrary()
	kid := uuid.New()
	// History alone: the kid's plays become play events (which mark the
	// items watched on their own), but no extra watched marks.
	p := BuildPlan(planSnapshot(), l.idx, map[string]uuid.UUID{"2": kid}, Options{History: true}, planHistory)
	if len(p.Marks) != 0 || len(p.Events) != 3 {
		t.Errorf("marks %d, events %d", len(p.Marks), len(p.Events))
	}
}

// A resume point the source gives no date for (Jellyfin, when a position
// was set without playing) gets the same fixed date on every run, so a
// re-run finds the event it wrote before instead of adding another.
func TestBuildPlan_UndatedResumePoint(t *testing.T) {
	l := newTestLibrary()
	owner := uuid.New()
	s := planSnapshot()
	s.States["1"] = []State{{Key: "heat", PositionMS: 600000}}
	p := BuildPlan(s, l.idx, map[string]uuid.UUID{"1": owner}, Options{Watched: true}, planHistory)
	if len(p.Events) != 1 || p.Events[0].Type != eventResume || !p.Events[0].At.Equal(unknownViewedAt) {
		t.Errorf("events = %+v, want one resume point dated %v", p.Events, unknownViewedAt)
	}
}
