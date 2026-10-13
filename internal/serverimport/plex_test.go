package serverimport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

const plexTestToken = "owner-token"

// fakePlex is a Plex Media Server with one movie library, one TV library,
// one music library, two accounts, play history and a playlist.
type fakePlex struct {
	*httptest.Server
	ownerOnly bool // /accounts refuses the token (not the owner's)
	pageCalls map[string]int
}

func meta(fields map[string]any) map[string]any { return fields }

func newFakePlex(t *testing.T) *fakePlex {
	t.Helper()
	f := &fakePlex{pageCalls: map[string]int{}}
	movies := []map[string]any{
		meta(map[string]any{"ratingKey": "10", "type": "movie", "title": "Alien", "duration": 7020000,
			"guid": "plex://movie/abc", "Guid": []map[string]string{{"id": "imdb://tt0078748"}, {"id": "tmdb://348"}},
			"viewCount": 2, "lastViewedAt": 1700000000, "userRating": 9.0,
			"Media": []map[string]any{{"Part": []map[string]string{{"file": "/data/movies/Alien (1979)/Alien.mkv"}}}}}),
		meta(map[string]any{"ratingKey": "11", "type": "movie", "title": "Heat", "duration": 10200000,
			"guid": "com.plexapp.agents.imdb://tt0113277?lang=en", "viewOffset": 600000, "lastViewedAt": 1710000000}),
		meta(map[string]any{"ratingKey": "12", "type": "movie", "title": "Unwatched"}),
	}
	shows := []map[string]any{
		meta(map[string]any{"ratingKey": "20", "type": "show", "title": "Severance", "Guid": []map[string]string{{"id": "tvdb://371980"}},
			"Location": []map[string]string{{"path": "/data/tv/Severance"}}}),
	}
	episodes := []map[string]any{
		meta(map[string]any{"ratingKey": "21", "type": "episode", "title": "Good News About Hell", "grandparentRatingKey": "20",
			"parentIndex": 1, "index": 1, "viewCount": 1, "lastViewedAt": 1690000000, "Guid": []map[string]string{{"id": "tvdb://7654321"}}}),
		meta(map[string]any{"ratingKey": "22", "type": "episode", "title": "Legacy", "grandparentRatingKey": "99",
			"guid": "com.plexapp.agents.thetvdb://81189/2/3?lang=en", "parentIndex": 2, "index": 3, "viewCount": 1}),
	}
	tracks := []map[string]any{
		meta(map[string]any{"ratingKey": "30", "type": "track", "title": "Song", "userRating": 8.0,
			"Media": []map[string]any{{"Part": []map[string]string{{"file": "/music/Artist/Album/01 Song.flac"}}}}}),
	}
	history := []map[string]any{
		{"ratingKey": "10", "viewedAt": 1700000000, "accountID": 1},
		{"ratingKey": "21", "viewedAt": 1690000000, "accountID": 2},
		{"ratingKey": "10", "viewedAt": 1600000000, "accountID": 3}, // unmapped account
		{"viewedAt": 1600000000, "accountID": 1},                    // deleted item: no ratingKey
	}
	page := func(w http.ResponseWriter, r *http.Request, all []map[string]any) {
		start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		size, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
		if size == 0 {
			size = len(all)
		}
		end := min(start+size, len(all))
		if start > len(all) {
			start = end
		}
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{
			"size": end - start, "totalSize": len(all), "Metadata": all[start:end]}})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{
			"friendlyName": "Den", "machineIdentifier": "mach-1", "version": "1.40"}})
	})
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		if f.ownerOnly {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{"Account": []map[string]any{
			{"id": 0, "name": ""}, {"id": 1, "name": "Owner"}, {"id": 2, "name": "Kid"}, {"id": 3, "name": "Guest"}}}})
	})
	mux.HandleFunc("/library/sections", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"MediaContainer": map[string]any{"Directory": []map[string]any{
			{"key": "1", "type": "movie", "title": "Movies"}, {"key": "2", "type": "show", "title": "TV"},
			{"key": "3", "type": "artist", "title": "Music"}, {"key": "4", "type": "photo", "title": "Photos"}}}})
	})
	mux.HandleFunc("/library/sections/{key}/all", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("includeGuids") != "1" {
			t.Errorf("library list without includeGuids: %s", r.URL)
		}
		f.pageCalls[r.PathValue("key")+"/"+r.URL.Query().Get("type")]++
		switch r.PathValue("key") + "/" + r.URL.Query().Get("type") {
		case "1/1":
			page(w, r, movies)
		case "2/2":
			page(w, r, shows)
		case "2/4":
			page(w, r, episodes)
		case "3/10":
			page(w, r, tracks)
		default:
			t.Errorf("unexpected library list %s", r.URL)
			page(w, r, nil)
		}
	})
	mux.HandleFunc("/status/sessions/history/all", func(w http.ResponseWriter, r *http.Request) { page(w, r, history) })
	mux.HandleFunc("/playlists", func(w http.ResponseWriter, r *http.Request) {
		page(w, r, []map[string]any{
			{"ratingKey": "40", "title": "Favourites", "playlistType": "video"},
			{"ratingKey": "41", "title": "Pics", "playlistType": "photo"},
		})
	})
	mux.HandleFunc("/playlists/40/items", func(w http.ResponseWriter, r *http.Request) {
		page(w, r, []map[string]any{{"ratingKey": "11"}, {"ratingKey": "10"}})
	})
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != plexTestToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("request without Accept: application/json: %s", r.URL)
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func noProgress(string) {}

func TestPlexProbe(t *testing.T) {
	f := newFakePlex(t)
	info, accts, err := NewPlex(f.URL, plexTestToken).Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info != (ServerInfo{ID: "mach-1", Name: "Den", Version: "1.40"}) {
		t.Errorf("info = %+v", info)
	}
	want := []Account{{ID: "1", Name: "Owner", FullState: true}, {ID: "2", Name: "Kid"}, {ID: "3", Name: "Guest"}}
	if len(accts) != len(want) {
		t.Fatalf("accounts = %+v", accts)
	}
	for i := range want {
		if accts[i] != want[i] {
			t.Errorf("account %d = %+v, want %+v", i, accts[i], want[i])
		}
	}

	if _, _, err := NewPlex(f.URL, "wrong").Probe(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("wrong token: %v, want ErrUnauthorized", err)
	}
	f.ownerOnly = true
	var fe *FetchError
	if _, _, err := NewPlex(f.URL, plexTestToken).Probe(context.Background()); !errors.As(err, &fe) {
		t.Errorf("non-owner token: %v, want a FetchError explaining it", err)
	}
}

func TestPlexFetch(t *testing.T) {
	f := newFakePlex(t)
	all := Options{Watched: true, Ratings: true, Playlists: true, History: true}
	snap, err := NewPlex(f.URL, plexTestToken).Fetch(context.Background(), []string{"1", "2"}, all, noProgress)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	alien := snap.Items["10"]
	if alien == nil || alien.Kind != ItemMovie || alien.IDs.IMDB != "tt0078748" || alien.IDs.TMDB != 348 ||
		alien.Path != "/data/movies/Alien (1979)/Alien.mkv" || alien.DurationMS != 7020000 {
		t.Errorf("Alien = %+v", alien)
	}
	if heat := snap.Items["11"]; heat == nil || heat.IDs.IMDB != "tt0113277" {
		t.Errorf("Heat (legacy GUID) = %+v", heat)
	}
	ep := snap.Items["21"]
	if ep == nil || ep.Kind != ItemEpisode || ep.ShowIDs.TVDB != 371980 || !ep.HasSE || ep.Season != 1 || ep.Episode != 1 {
		t.Errorf("episode = %+v", ep)
	}
	if legacy := snap.Items["22"]; legacy == nil || legacy.ShowIDs.TVDB != 81189 || legacy.Season != 2 || legacy.Episode != 3 {
		t.Errorf("legacy-agent episode = %+v", legacy)
	}
	if sh := snap.Items["20"]; sh == nil || sh.Kind != ItemShow || sh.Path != "/data/tv/Severance" {
		t.Errorf("show = %+v (its folder is its path)", sh)
	}
	if tr := snap.Items["30"]; tr == nil || tr.Kind != ItemTrack {
		t.Errorf("track = %+v", tr)
	}

	// The owner's per-item state: watched Alien (rated 9), Heat in
	// progress, the two episodes watched, the track rated.
	states := map[string]State{}
	for _, st := range snap.States["1"] {
		states[st.Key] = st
	}
	if st := states["10"]; !st.Played || st.Rating == nil || *st.Rating != 9 || !st.LastViewedAt.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("Alien state = %+v", st)
	}
	if st := states["11"]; st.Played || st.PositionMS != 600000 {
		t.Errorf("Heat state = %+v", st)
	}
	if _, ok := states["12"]; ok {
		t.Error("an untouched movie has a state")
	}
	if len(snap.States) != 1 {
		t.Errorf("states for accounts %v; only the owner's are visible", sortedKeys(snap.States))
	}

	// History: mapped accounts only, and only plays of items still there.
	if len(snap.Plays) != 2 {
		t.Fatalf("plays = %+v", snap.Plays)
	}
	if snap.Plays[1] != (Play{Account: "2", Key: "21", At: time.Unix(1690000000, 0).UTC()}) {
		t.Errorf("kid's play = %+v", snap.Plays[1])
	}

	pls := snap.Playlists["1"]
	if len(pls) != 1 || pls[0].Name != "Favourites" || len(pls[0].Keys) != 2 || pls[0].Keys[0] != "11" {
		t.Errorf("playlists = %+v (photo playlists are skipped)", pls)
	}
}

func TestPlexFetch_SkipsWhatIsntNeeded(t *testing.T) {
	f := newFakePlex(t)
	// Only the owner, watched state only: no music, no history, no playlists.
	snap, err := NewPlex(f.URL, plexTestToken).Fetch(context.Background(), []string{"1"}, Options{Watched: true}, noProgress)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if f.pageCalls["3/10"] != 0 {
		t.Error("read the music library for a watched-only import")
	}
	if len(snap.Plays) != 0 || len(snap.Playlists) != 0 {
		t.Errorf("plays %d, playlists %d; want none", len(snap.Plays), len(snap.Playlists))
	}
}

func TestPlexFetch_Pages(t *testing.T) {
	old := plexPageSize
	plexPageSize = 2
	t.Cleanup(func() { plexPageSize = old })
	f := newFakePlex(t)
	snap, err := NewPlex(f.URL, plexTestToken).Fetch(context.Background(), []string{"1"}, Options{Watched: true}, noProgress)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if f.pageCalls["1/1"] != 2 {
		t.Errorf("movie list read in %d requests, want 2 pages of 2", f.pageCalls["1/1"])
	}
	for _, k := range []string{"10", "11", "12"} {
		if snap.Items[k] == nil {
			t.Errorf("movie %s missing after paging", k)
		}
	}
}

func TestParsePlexGUID(t *testing.T) {
	cases := []struct {
		guid   string
		want   ExternalIDs
		legacy int
	}{
		{"imdb://tt0111161", ExternalIDs{IMDB: "tt0111161"}, 0},
		{"tmdb://603", ExternalIDs{TMDB: 603}, 0},
		{"tvdb://81189", ExternalIDs{TVDB: 81189}, 0},
		{"com.plexapp.agents.imdb://tt0111161?lang=en", ExternalIDs{IMDB: "tt0111161"}, 0},
		{"com.plexapp.agents.themoviedb://603?lang=en", ExternalIDs{TMDB: 603}, 0},
		{"com.plexapp.agents.thetvdb://81189?lang=en", ExternalIDs{TVDB: 81189}, 0},
		{"com.plexapp.agents.thetvdb://81189/1/2?lang=en", ExternalIDs{}, 81189},
		{"plex://movie/5d776825880197001ec967c8", ExternalIDs{}, 0},
		{"com.plexapp.agents.hama://anidb-123?lang=en", ExternalIDs{}, 0},
		{"imdb://nm123", ExternalIDs{}, 0},
		{"tmdb://x", ExternalIDs{}, 0},
		{"no-scheme", ExternalIDs{}, 0},
	}
	for _, c := range cases {
		var got ExternalIDs
		legacy := parsePlexGUID(c.guid, &got)
		if got != c.want || legacy != c.legacy {
			t.Errorf("%s: %+v / %d, want %+v / %d", c.guid, got, legacy, c.want, c.legacy)
		}
	}
}
